"use client";

import Link from "next/link";
import { useMemo } from "react";
import { ArrowRight, BrainCircuit, Gauge, ScanSearch, Target, Wind, Zap } from "lucide-react";
import { Bar, BarChart, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { axisTick, cursorFill, tooltipStyle } from "@/lib/chart-theme";
import { SeriesChart } from "@/components/charts/series-chart";
import { MODEL_LABEL, useModelStatus } from "@/lib/forecast";
import { congestionColor, fmt, hhmm, pmColor, pmLabel, utilColor } from "@/lib/format";
import { useLive } from "@/lib/store";
import type { AccuracySummary, ModelStatus } from "@/lib/types";
import { Card as NxCard } from "@/components/ui/nx";
import { cn } from "@/lib/utils";

const EPOCH_MS = Date.UTC(2026, 5, 1, 5, 30);
const tickTime = (tick: number) => new Date(EPOCH_MS + tick * 1000).toISOString();

function Panel({ title, icon, children, className, aside }: { title: string; icon: typeof Gauge; children: React.ReactNode; className?: string; aside?: React.ReactNode }) {
  return (
    <NxCard title={title} icon={icon} action={aside} className={className}>
      {children}
    </NxCard>
  );
}

function Empty({ text }: { text: string }) {
  return <p className="rounded-lg border border-dashed border-border p-3 text-xs text-muted-foreground">{text}</p>;
}

function ModelChips({ status }: { status: ModelStatus[] | null }) {
  return (
    <div className="flex flex-wrap gap-2">
      {(status ?? []).map((m) => (
        <div key={m.kind} className="flex items-center gap-2 rounded-md border border-border bg-raised px-3 py-1 text-xs">
          <span className={cn("size-1.5 rounded-full", m.alive ? "bg-ok" : "bg-muted-foreground/50")} />
          <span>{MODEL_LABEL[m.kind]}</span>
          <span className="font-mono text-[12px] text-muted-foreground">
            {m.service?.version ? m.service.version.split("-").slice(1).join("-") : m.service?.error ? "not trained" : "offline"}
          </span>
          {m.alive && m.service?.latency_ms != null && (
            <span className="font-mono text-[12px] text-muted-foreground">{m.service.latency_ms.toFixed(0)} ms</span>
          )}
        </div>
      ))}
    </div>
  );
}

function LiveAccuracy({ status }: { status: ModelStatus[] | null }) {
  const rows = (status ?? []).filter((m) => m.kind !== "anomaly");
  const fmtErr = (kind: string, v: number) => (kind === "traffic" ? v.toFixed(3) : kind === "energy" ? `${v.toFixed(2)} MW` : `${v.toFixed(2)} µg`);
  const baselineName: Record<string, string> = { traffic: "persistence", energy: "same time yesterday", pm25: "persistence" };
  return (
    <div className="space-y-3">
      <p className="text-[12px] leading-relaxed text-muted-foreground">
        Every forecast is scored when its target time arrives, against what the simulation then did — on this live city (seed 42),
        which no model was trained on. Window: last 6 simulated hours.
      </p>
      {rows.map((m) => (
        <div key={m.kind}>
          <div className="mb-1 text-xs font-medium">{MODEL_LABEL[m.kind]}</div>
          {m.live_accuracy.length === 0 ? (
            <p className="text-[12px] text-muted-foreground">Collecting — forecasts need to reach their target time first.</p>
          ) : (
            <table className="w-full text-[12px]">
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="font-normal">Horizon</th>
                  <th className="font-normal">Model MAE</th>
                  <th className="font-normal">{baselineName[m.kind]}</th>
                  <th className="font-normal">Scored</th>
                </tr>
              </thead>
              <tbody>
                {m.live_accuracy.filter((a) => m.kind !== "energy" || [1, 3, 6, 12, 24].includes(a.horizon)).map((a: AccuracySummary) => {
                  const better = a.baseline_mae != null && a.model_mae < a.baseline_mae;
                  return (
                    <tr key={a.horizon} className="border-t border-border/50">
                      <td className="py-1 font-mono">+{a.horizon}{m.kind === "energy" ? " h" : " min"}</td>
                      <td className={cn("font-mono", a.baseline_mae != null && (better ? "text-ok" : "text-warn"))}>{fmtErr(m.kind, a.model_mae)}</td>
                      <td className="font-mono text-muted-foreground">{a.baseline_mae != null ? fmtErr(m.kind, a.baseline_mae) : "needs 24 h"}</td>
                      <td className="font-mono text-muted-foreground">{a.points}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      ))}
    </div>
  );
}

export function Predictions() {
  const city = useLive((s) => s.city);
  const t = useLive((s) => s.telemetry);
  const forecasts = useLive((s) => s.forecasts);
  const anomalies = useLive((s) => s.anomalies);
  const { status, error } = useModelStatus();

  const roads = useMemo(() => {
    const f = forecasts.traffic?.payload;
    if (!f || !city || !t) return [];
    return f.entities
      .map((id, i) => {
        const road = city.roads.find((r) => r.id === id)!;
        return {
          road,
          now: t.roads.find((r) => r.id === id)?.congestion ?? 0,
          h15: f.values["15"][i],
          h30: f.values["30"][i],
          h60: f.values["60"][i],
        };
      })
      .sort((a, b) => b.h30 - a.h30)
      .slice(0, 10);
  }, [forecasts.traffic, city, t]);

  const energy = useMemo(() => {
    const f = forecasts.energy?.payload;
    if (!f || !city) return null;
    const hours = f.horizons;
    const total = hours.map((h) => ({ t: tickTime(f.issued_tick + h * 3600), demand: f.values[String(h)].reduce((a, b) => a + b, 0) }));
    const capacity = city.districts.reduce((a, d) => a + d.substation_mw, 0);
    const districts = f.entities.map((id, i) => {
      const d = city.districts.find((x) => x.id === id)!;
      let peakH = hours[0];
      let peak = -1;
      let tonight = -1;
      for (const h of hours) {
        const v = f.values[String(h)][i];
        if (v > peak) [peak, peakH] = [v, h];
        const hour = new Date(EPOCH_MS + (f.issued_tick + h * 3600) * 1000).getUTCHours();
        if (hour >= 18 && v > tonight) tonight = v;
      }
      return { d, peak, peakAt: tickTime(f.issued_tick + peakH * 3600), util: peak / d.substation_mw, tonight };
    });
    const tonightMax = districts.reduce((a, b) => (b.tonight > a.tonight ? b : a));
    return { total: total.map((r) => ({ ...r, cap: capacity })), districts: districts.sort((a, b) => b.util - a.util), tonightMax, issued: f.issued_tick };
  }, [forecasts.energy, city]);

  const air = useMemo(() => {
    const f = forecasts.pm25?.payload;
    if (!f || !city || !t) return [];
    return f.entities.map((id, i) => {
      const fac = city.facilities.find((x) => x.id === id)!;
      const st = t.stations.find((s) => s.id === id);
      return { name: city.districts[fac.district].id, full: fac.name, id, now: st?.status === "ok" ? st.pm25 : null, h60: f.values["60"][i] };
    });
  }, [forecasts.pm25, city, t]);

  const nothing = !forecasts.traffic && !forecasts.energy && !forecasts.pm25 && !anomalies;

  return (
    <div className="mx-auto max-w-[1600px] space-y-5 px-4 py-6 sm:px-6">
      <div className="flex flex-wrap items-end gap-4">
        <div>
          <div className="eyebrow">AI predictions</div>
          <h1 className="mt-1 text-[22px] font-semibold tracking-[-0.01em]">What the models expect next</h1>
          <p className="mt-1.5 max-w-3xl text-[13.5px] leading-relaxed text-muted-foreground">
            XGBoost forecasters and Isolation Forest detectors trained on other simulated cities, running live against this one.
            Synthetic demonstrations — not real-world forecasts.{" "}
            <Link href="/models" className="text-brand hover:underline">
              How good are they?
            </Link>
          </p>
        </div>
        <div className="ml-auto">
          <ModelChips status={status} />
          {error && <p className="mt-1 text-xs text-crit">{error}</p>}
        </div>
      </div>

      {nothing && (
        <Empty text="No model service is publishing yet. Train the models (nexus-ml train all) and start the inference service (nexus-ml serve) — see the README. Forecasts appear within a few simulated minutes." />
      )}

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-12">
        <Panel title="Roads needing attention — next 30 minutes" icon={Gauge} className="xl:col-span-7">
          {roads.length === 0 ? (
            <Empty text="Waiting for the traffic model." />
          ) : (
            <table className="w-full text-xs">
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="label-caps pb-2 font-normal">Road</th>
                  {["Now", "+15", "+30", "+60"].map((h) => (
                    <th key={h} className="label-caps pb-2 text-right font-normal">{h}</th>
                  ))}
                  <th />
                </tr>
              </thead>
              <tbody>
                {roads.map(({ road, now, h15, h30, h60 }) => (
                  <tr key={road.id} className="border-t border-border/60">
                    <td className="max-w-[260px] truncate py-1.5 pr-2">
                      {road.name} <span className="text-muted-foreground">· {road.segment}</span>
                    </td>
                    {[now, h15, h30, h60].map((v, k) => (
                      <td key={k} className="text-right font-mono num" style={{ color: congestionColor(v) }}>
                        {fmt.pct(v)}
                      </td>
                    ))}
                    <td className="pl-2 text-right">
                      <Link href={`/twin?select=road:${road.id}`} className="inline-flex items-center gap-1 text-[12px] text-brand hover:underline">
                        map <ArrowRight className="size-3" />
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>

        <Panel title="Live accuracy on this run" icon={Target} className="xl:col-span-5">
          <LiveAccuracy status={status} />
        </Panel>

        <Panel title="Electricity demand — next 24 hours" icon={Zap} className="xl:col-span-7"
          aside={energy && <span className="font-mono text-[12px] text-muted-foreground">issued {hhmm(tickTime(energy.issued))}</span>}>
          {!energy ? (
            <Empty text="Waiting for the energy model (it forecasts on the hour)." />
          ) : (
            <>
              <p className="mb-2 text-xs">
                Highest demand tonight (18:00–24:00):{" "}
                <span className="font-medium text-brand">{energy.tonightMax.d.id} · {energy.tonightMax.d.name}</span>, peaking near{" "}
                <span className="font-mono">{fmt.num(energy.tonightMax.tonight)} MW</span>.
              </p>
              <SeriesChart
                height={180}
                data={energy.total}
                leftDomain={[0, "auto"]}
                series={[
                  { key: "demand", label: "City demand forecast (MW)", color: "#4c8dff", format: (v) => v.toFixed(0) },
                  { key: "cap", label: "Total substation capacity", color: "#f0565b", type: "line", dashed: true, format: (v) => v.toFixed(0) },
                ]}
              />
              <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-[12px] sm:grid-cols-3">
                {energy.districts.slice(0, 6).map(({ d, peak, peakAt, util }) => (
                  <div key={d.id} className="flex items-center gap-2">
                    <span className="w-8 font-mono text-brand/80">{d.id}</span>
                    <span className="font-mono num" style={{ color: utilColor(util) }}>{fmt.pct(util)}</span>
                    <span className="truncate text-muted-foreground">peak {fmt.num(peak, 0)} MW at {hhmm(peakAt)}</span>
                  </div>
                ))}
              </div>
            </>
          )}
        </Panel>

        <Panel title="Air quality outlook — next hour" icon={Wind} className="xl:col-span-5">
          {air.length === 0 ? (
            <Empty text="Waiting for the PM2.5 model." />
          ) : (
            <>
              <div className="h-[180px]">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={air} margin={{ top: 4, right: 4, bottom: 0, left: -18 }}>
                    <XAxis dataKey="name" tick={axisTick} axisLine={false} tickLine={false} />
                    <YAxis tick={axisTick} axisLine={false} tickLine={false} />
                    <Tooltip
                      cursor={cursorFill}
                      contentStyle={tooltipStyle}
                      labelFormatter={(_, p) => (p?.[0]?.payload as { full?: string })?.full ?? ""}
                      formatter={(v, n) => [v == null ? "no valid reading" : `${Number(v).toFixed(1)} µg/m³`, n === "now" ? "Now" : "+60 min forecast"]}
                    />
                    <Bar dataKey="now" fill="rgb(255 255 255 / 0.25)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
                    <Bar dataKey="h60" radius={[2, 2, 0, 0]} isAnimationActive={false}>
                      {air.map((r) => (
                        <Cell key={r.id} fill={pmColor(r.h60)} fillOpacity={0.85} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
              <p className="mt-1 text-[12px] text-muted-foreground">
                Pale bar = current reading, coloured bar = forecast in 60 min.{" "}
                {air.filter((a) => a.h60 >= 35.5).length > 0
                  ? `${air.filter((a) => a.h60 >= 35.5).length} station(s) forecast at or above "${pmLabel(35.5)}".`
                  : "No station is forecast to reach unhealthy-for-sensitive-groups levels."}
              </p>
            </>
          )}
        </Panel>

        <Panel title="Anomaly detector (Isolation Forest)" icon={ScanSearch} className="xl:col-span-12"
          aside={anomalies && <span className="font-mono text-[12px] text-muted-foreground">scored {hhmm(tickTime(anomalies.payload.issued_tick))}</span>}>
          {!anomalies ? (
            <Empty text="Waiting for the anomaly service." />
          ) : (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              {(["traffic", "energy", "air"] as const).map((domain) => {
                const flagged = anomalies.payload.flagged.filter((a) => (domain === "traffic" ? a.entity_type === "road" : domain === "energy" ? a.entity_type === "district" : a.entity_type === "station"));
                return (
                  <div key={domain}>
                    <div className="mb-1.5 flex items-baseline justify-between text-xs">
                      <span className="font-medium capitalize">{domain === "air" ? "Air-quality stations" : domain === "traffic" ? "Roads" : "Districts (energy)"}</span>
                      <span className="font-mono text-[12px] text-muted-foreground">{flagged.length} flagged</span>
                    </div>
                    {flagged.length === 0 ? (
                      <p className="text-[12px] text-muted-foreground">Nothing above the alert threshold.</p>
                    ) : (
                      <ul className="max-h-48 space-y-1 overflow-y-auto pr-1">
                        {flagged.slice(0, 12).map((a) => (
                          <li key={a.entity_id} className="rounded-md border border-violet-400/20 bg-violet-400/5 px-2 py-1 text-[12px]">
                            <span className="font-mono text-violet-300">{a.entity_id}</span> <span className="text-muted-foreground">{a.reason}</span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                );
              })}
              <p className="text-[12px] leading-relaxed text-muted-foreground md:col-span-3">
                <BrainCircuit className="mr-1 inline size-3 text-brand" />
                Unsupervised detector, shown as a second opinion beside the rule-based alerts. On the held-out benchmark the simple rules
                score higher F1 for air quality and energy — see the Model Lab for the full comparison.
              </p>
            </div>
          )}
        </Panel>
      </div>
    </div>
  );
}
