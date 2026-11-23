"use client";

import dynamic from "next/dynamic";
import { useCallback, useEffect, useMemo, useState } from "react";
import { motion } from "motion/react";
import { BrainCircuit, CloudSun, Construction, Factory, FlaskConical, Info, Loader2, MapPin, Play, TrafficCone, Users } from "lucide-react";
import { SeriesChart } from "@/components/charts/series-chart";
import { Slider } from "@/components/ui/slider";
import { api } from "@/lib/api";
import { fmt, hhmm, simClock } from "@/lib/format";
import { useLive } from "@/lib/store";
import type { BranchSummary, City, Scenario, ScenarioKind, ScenarioParams, ScenarioResult } from "@/lib/types";
import { cn } from "@/lib/utils";

const DiffMap = dynamic(() => import("./diff-map").then((m) => m.DiffMap), { ssr: false });

const EPOCH_MS = Date.UTC(2026, 5, 1, 5, 30);
const tickTime = (tick: number) => new Date(EPOCH_MS + tick * 1000).toISOString();

const KINDS: { kind: ScenarioKind; title: string; blurb: string; icon: typeof Construction }[] = [
  { kind: "road_closure", title: "Road closure", blurb: "Close segments; traffic re-routes, including vehicles already driving.", icon: Construction },
  { kind: "rush_hour", title: "Rush-hour surge", blurb: "More trips start than usual.", icon: Users },
  { kind: "signal_timing", title: "Signal timing", blurb: "Adaptive green splits that follow queues, vs today's fixed plans.", icon: TrafficCone },
  { kind: "industrial_spike", title: "Industrial spike", blurb: "Raise emissions from one industrial district.", icon: Factory },
  { kind: "heatwave", title: "Heatwave", blurb: "Hotter weather drives cooling demand.", icon: CloudSun },
];
const KIND_ICON = Object.fromEntries(KINDS.map((k) => [k.kind, k.icon])) as Record<ScenarioKind, typeof Construction>;

// ------------------------------------------------------------------ builder

function Builder({ city, onCreated }: { city: City; onCreated: (s: Scenario) => void }) {
  const telemetry = useLive((s) => s.telemetry);
  const simTime = useLive((s) => s.simTime);
  const [kind, setKind] = useState<ScenarioKind>("road_closure");
  const [roads, setRoads] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [demand, setDemand] = useState(30);
  const [district, setDistrict] = useState(city.districts.find((d) => d.type === "industrial")?.id ?? "D1");
  const [factor, setFactor] = useState(4);
  const [temp, setTemp] = useState(6);
  const [duration, setDuration] = useState(120);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const busiest = useMemo(() => {
    if (!telemetry) return [];
    return telemetry.roads
      .map((o, i) => ({ o, r: city.roads[i] }))
      .filter((x) => x.r.class !== "local")
      .sort((a, b) => b.o.flow_vph - a.o.flow_vph)
      .slice(0, 5)
      .map((x) => x.r);
  }, [telemetry, city]);
  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    return city.roads.filter((r) => `${r.id} ${r.name} ${r.segment}`.toLowerCase().includes(q)).slice(0, 8);
  }, [query, city]);

  const toggleRoad = (id: string) => setRoads((rs) => (rs.includes(id) ? rs.filter((x) => x !== id) : rs.length >= 24 ? rs : [...rs, id]));
  const params: ScenarioParams =
    kind === "road_closure" ? { road_ids: roads }
    : kind === "rush_hour" ? { demand_pct: demand }
    : kind === "signal_timing" ? { policy: "adaptive" }
    : kind === "industrial_spike" ? { district_id: district, emission_factor: factor }
    : { temp_delta_c: temp };
  const ready = kind !== "road_closure" || roads.length > 0;

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      onCreated(await api.createScenario({ kind, params, duration_min: duration }));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const c = simClock(simTime);
  const one = (v: number | readonly number[]) => (Array.isArray(v) ? v[0] : (v as number));

  return (
    <section className="panel space-y-4 rounded-xl p-4">
      <div>
        <div className="eyebrow">New experiment</div>
        <p className="mt-1 text-[12px] text-muted-foreground">
          Branches from the live city at <span className="font-mono text-foreground">{c.day} {c.time}</span> and runs both the unchanged
          baseline and your change for the same simulated time.
        </p>
      </div>
      <div className="grid grid-cols-1 gap-1.5">
        {KINDS.map(({ kind: k, title, blurb, icon: Icon }) => (
          <button
            key={k}
            type="button"
            onClick={() => setKind(k)}
            aria-pressed={kind === k}
            className={cn(
              "flex items-start gap-2.5 rounded-lg border px-3 py-2 text-left transition",
              kind === k ? "border-brand/50 bg-brand/10" : "border-border bg-raised hover:bg-accent/50",
            )}
          >
            <Icon className={cn("mt-0.5 size-4 shrink-0", kind === k ? "text-brand" : "text-muted-foreground")} />
            <span>
              <span className="block text-xs font-medium">{title}</span>
              <span className="block text-[12px] text-muted-foreground">{blurb}</span>
            </span>
          </button>
        ))}
      </div>

      <div className="space-y-2 rounded-lg border border-border bg-raised p-3 text-xs">
        {kind === "road_closure" && (
          <>
            <div className="label-caps">Roads to close ({roads.length})</div>
            {roads.length > 0 && (
              <div className="flex flex-wrap gap-1">
                {roads.map((id) => {
                  const r = city.roads.find((x) => x.id === id)!;
                  return (
                    <button key={id} type="button" onClick={() => toggleRoad(id)} className="rounded-full border border-brand/40 bg-brand/10 px-2 py-0.5 text-[12px]">
                      {r.name} · {r.segment} ✕
                    </button>
                  );
                })}
              </div>
            )}
            <div className="text-[12px] text-muted-foreground">Busiest major roads right now:</div>
            <div className="flex flex-col gap-0.5">
              {busiest.map((r) => (
                <label key={r.id} className="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 hover:bg-accent/50">
                  <input type="checkbox" checked={roads.includes(r.id)} onChange={() => toggleRoad(r.id)} className="accent-brand-400" />
                  <span className="truncate">{r.name} <span className="text-muted-foreground">· {r.segment}</span></span>
                </label>
              ))}
            </div>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search any road (name, cross street or id)…"
              className="w-full rounded-md border border-border bg-raised px-2 py-1.5 text-xs outline-none focus:border-brand/50"
              aria-label="Search roads"
            />
            {matches.map((r) => (
              <label key={r.id} className="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 hover:bg-accent/50">
                <input type="checkbox" checked={roads.includes(r.id)} onChange={() => toggleRoad(r.id)} className="accent-brand-400" />
                <span className="truncate">{r.name} <span className="text-muted-foreground">· {r.segment} · {r.id}</span></span>
              </label>
            ))}
          </>
        )}
        {kind === "rush_hour" && (
          <>
            <div className="flex justify-between"><span className="label-caps">Extra trips</span><span className="font-mono">+{demand}%</span></div>
            <Slider min={5} max={150} step={5} value={[demand]} onValueChange={(v: number | readonly number[]) => setDemand(one(v))} aria-label="Extra trips percent" />
          </>
        )}
        {kind === "signal_timing" && (
          <p className="flex gap-2 text-[12px] leading-relaxed text-muted-foreground">
            <Info className="mt-0.5 size-3.5 shrink-0" />
            Every minute each signal re-splits its cycle in proportion to the vehicles queued on each approach (kept between 25% and 75%),
            compared with the city&apos;s fixed plans.
          </p>
        )}
        {kind === "industrial_spike" && (
          <>
            <div className="label-caps">Industrial district</div>
            <div className="flex gap-1.5">
              {city.districts.filter((d) => d.type === "industrial").map((d) => (
                <button key={d.id} type="button" onClick={() => setDistrict(d.id)} aria-pressed={district === d.id}
                  className={cn("rounded-md border px-2 py-1", district === d.id ? "border-brand/50 bg-brand/10" : "border-border")}>
                  {d.id} · {d.name}
                </button>
              ))}
            </div>
            <div className="flex justify-between pt-1"><span className="label-caps">Emissions</span><span className="font-mono">×{factor.toFixed(1)}</span></div>
            <Slider min={1.5} max={10} step={0.5} value={[factor]} onValueChange={(v: number | readonly number[]) => setFactor(one(v))} aria-label="Emission multiplier" />
          </>
        )}
        {kind === "heatwave" && (
          <>
            <div className="flex justify-between"><span className="label-caps">Hotter by</span><span className="font-mono">+{temp} °C</span></div>
            <Slider min={1} max={15} step={1} value={[temp]} onValueChange={(v: number | readonly number[]) => setTemp(one(v))} aria-label="Temperature increase" />
          </>
        )}
      </div>

      <div>
        <div className="label-caps mb-1.5">Simulated duration</div>
        <div className="flex gap-1" role="group" aria-label="Duration">
          {[60, 120, 180, 240, 360].map((d) => (
            <button key={d} type="button" onClick={() => setDuration(d)} aria-pressed={duration === d}
              className={cn("flex-1 rounded-md border py-1 font-mono text-xs", duration === d ? "border-brand/50 bg-brand/10 text-brand" : "border-border text-muted-foreground")}>
              {d / 60} h
            </button>
          ))}
        </div>
      </div>
      {error && <p className="text-xs text-crit">{error}</p>}
      <button type="button" onClick={submit} disabled={!ready || busy}
        className="flex w-full items-center justify-center gap-2 rounded-lg bg-primary py-2 text-sm font-medium text-primary-foreground transition hover:bg-primary/85 disabled:opacity-40">
        {busy ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />} Run experiment
      </button>
    </section>
  );
}

// ------------------------------------------------------------------ list

function headline(s: Scenario): string | null {
  const r = s.result as { baseline: BranchSummary; scenario: BranchSummary } | null | undefined;
  if (!r) return null;
  const d = (a: number, b: number) => (b - a >= 0 ? "+" : "") + (b - a).toFixed(1);
  switch (s.kind) {
    case "road_closure":
    case "rush_hour":
    case "signal_timing":
      return `trips ${d(r.baseline.avg_trip_min, r.scenario.avg_trip_min)} min`;
    case "industrial_spike":
      return `PM2.5 ${d(r.baseline.avg_pm25, r.scenario.avg_pm25)} µg/m³`;
    case "heatwave":
      return `energy ${d(r.baseline.energy_mwh, r.scenario.energy_mwh)} MWh`;
  }
}

function RunList({ items, selected, onSelect }: { items: Scenario[]; selected: string | null; onSelect: (id: string) => void }) {
  const updates = useLive((s) => s.scenarioUpdates);
  if (items.length === 0) return <p className="p-3 text-xs text-muted-foreground">No experiments yet. Build one on the left.</p>;
  return (
    <ul className="space-y-1.5">
      {items.map((s) => {
        const u = updates[s.id];
        const status = u?.status ?? s.status;
        const progress = u?.progress ?? s.progress;
        const Icon = KIND_ICON[s.kind];
        const h = headline(s);
        return (
          <li key={s.id}>
            <button type="button" onClick={() => onSelect(s.id)}
              className={cn("w-full rounded-lg border px-3 py-2 text-left transition", selected === s.id ? "border-brand/50 bg-brand/10" : "border-border bg-raised hover:bg-accent/50")}>
              <div className="flex items-center gap-2 text-xs">
                <Icon className="size-3.5 shrink-0 text-brand/80" />
                <span className="flex-1 truncate font-medium">{s.name}</span>
                <span className={cn("font-mono text-[12px] uppercase", status === "done" ? "text-ok" : status === "failed" ? "text-crit" : "text-warn")}>{status}</span>
              </div>
              <div className="mt-1 flex items-center gap-2 text-[12px] text-muted-foreground">
                <span className="font-mono">{hhmm(tickTime(s.base_tick))} +{s.duration_min / 60}h</span>
                {status === "done" && h && <span className="ml-auto font-mono text-foreground/80">{h}</span>}
                {(status === "running" || status === "queued") && (
                  <span className="ml-auto flex items-center gap-2">
                    <span className="truncate">{u?.phase ?? s.phase ?? "waiting for a worker"}</span>
                    <span className="h-1 w-16 overflow-hidden rounded-full bg-secondary">
                      <span className="block h-full bg-warn transition-all" style={{ width: `${Math.round(progress * 100)}%` }} />
                    </span>
                  </span>
                )}
              </div>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

// ---------------------------------------------------------------- result

type MetricDef = { key: keyof BranchSummary; label: string; unit: string; better: "lower" | "higher" | "neutral"; format: (v: number) => string };

const METRICS: MetricDef[] = [
  { key: "avg_trip_min", label: "Average journey", unit: "min", better: "lower", format: (v) => v.toFixed(1) },
  { key: "avg_congestion", label: "Congestion index", unit: "", better: "lower", format: (v) => fmt.pct(v, 1) },
  { key: "avg_speed_kmh", label: "Network speed", unit: "km/h", better: "higher", format: (v) => v.toFixed(1) },
  { key: "trips_completed", label: "Trips completed", unit: "", better: "higher", format: (v) => fmt.int(v) },
  { key: "energy_mwh", label: "Electricity used", unit: "MWh", better: "lower", format: (v) => fmt.int(v) },
  { key: "max_utilization", label: "Peak substation load", unit: "", better: "lower", format: (v) => fmt.pct(v) },
  { key: "avg_pm25", label: "PM2.5 (city mean)", unit: "µg/m³", better: "lower", format: (v) => v.toFixed(1) },
  { key: "alerts_opened", label: "Alerts opened", unit: "", better: "lower", format: (v) => String(v) },
  { key: "rerouted", label: "Vehicles re-routed", unit: "", better: "neutral", format: (v) => fmt.int(v) },
];

function MetricCard({ m, base, scen }: { m: MetricDef; base: number; scen: number }) {
  const diff = scen - base;
  const rel = base !== 0 ? diff / Math.abs(base) : 0;
  const tiny = Math.abs(rel) < 0.005 && Math.abs(diff) < 1e-6 + (m.key === "alerts_opened" ? 0.5 : 0);
  const good = m.better === "neutral" || tiny ? null : (diff < 0) === (m.better === "lower");
  return (
    <div className="rounded-lg border border-border bg-raised px-3 py-2">
      <div className="label-caps">{m.label}</div>
      <div className="mt-1 flex flex-wrap items-baseline gap-x-2 font-mono text-sm num">
        <span className="text-muted-foreground">{m.format(base)}</span>
        <span className="text-muted-foreground">→</span>
        <span className="text-foreground">{m.format(scen)}</span>
        <span className="text-[12px] text-muted-foreground">{m.unit}</span>
      </div>
      <div className={cn("font-mono text-[12px]", good === null ? "text-muted-foreground" : good ? "text-ok" : "text-crit")}>
        {tiny ? "no change" : `${diff >= 0 ? "+" : ""}${m.key === "avg_congestion" || m.key === "max_utilization" ? (diff * 100).toFixed(1) + " pts" : m.format(diff)}${base ? ` (${rel >= 0 ? "+" : ""}${(rel * 100).toFixed(1)}%)` : ""}`}
      </div>
    </div>
  );
}

function ForecastTable({ f }: { f: ScenarioResult["forecasts"] }) {
  if (!f) return null;
  const rows: { model: string; what: string; base: string; scen: string; version?: string }[] = [];
  if (f.traffic) for (const h of ["15", "30", "60"]) rows.push({ model: "Traffic", what: `mean congestion +${h} min`, base: fmt.pct(f.traffic.baseline[h], 1), scen: fmt.pct(f.traffic.scenario[h], 1), version: f.traffic.version });
  if (f.energy) {
    rows.push({ model: "Energy", what: "city demand, next 24 h", base: `${fmt.int(f.energy.baseline_mwh_24h)} MWh`, scen: `${fmt.int(f.energy.scenario_mwh_24h)} MWh`, version: f.energy.version });
    rows.push({ model: "Energy", what: "peak hour, next 24 h", base: `${fmt.int(f.energy.baseline_peak_mw)} MW`, scen: `${fmt.int(f.energy.scenario_peak_mw)} MW` });
  }
  if (f.pm25) for (const h of ["30", "60"]) rows.push({ model: "PM2.5", what: `station mean +${h} min`, base: `${f.pm25.baseline[h].toFixed(1)} µg/m³`, scen: `${f.pm25.scenario[h].toFixed(1)} µg/m³`, version: f.pm25.version });
  return (
    <div className="space-y-2">
      {rows.length > 0 && (
        <table className="w-full text-xs [&_td]:pr-3 [&_th]:pr-3">
          <thead>
            <tr className="text-left text-muted-foreground">
              {["Model", "Forecast after the window", "Baseline", "Scenario"].map((h) => <th key={h} className="label-caps pb-1.5 font-normal">{h}</th>)}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i} className="border-t border-border/60">
                <td className="py-1.5">{r.model}{r.version && <span className="ml-1.5 font-mono text-[12px] text-muted-foreground">{r.version.split("-").slice(1).join("-")}</span>}</td>
                <td className="text-muted-foreground">{r.what}</td>
                <td className="font-mono num">{r.base}</td>
                <td className="font-mono num">{r.scen}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {f.errors && Object.keys(f.errors).length > 0 && (
        <p className="text-[12px] text-warn">
          Not available: {Object.entries(f.errors).map(([m, e]) => `${m} (${e.slice(0, 80)})`).join("; ")}. Start the model services to include their forecasts.
        </p>
      )}
    </div>
  );
}

function ResultView({ city, id }: { city: City; id: string }) {
  const update = useLive((s) => s.scenarioUpdates[id]);
  const [sc, setSc] = useState<Scenario | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [chart, setChart] = useState<"congestion" | "trip_min_1h" | "demand_mw" | "pm25">("congestion");

  const load = useCallback(() => {
    api.scenario(id).then(setSc, (e: Error) => setError(e.message));
  }, [id]);
  useEffect(load, [load]);
  useEffect(() => {
    if (update?.status === "done" || update?.status === "failed") load();
  }, [update?.status, load]);

  if (error) return <p className="p-4 text-sm text-crit">{error}</p>;
  if (!sc) return <div className="h-64 animate-pulse rounded-xl bg-raised" />;
  const status = update?.status ?? sc.status;
  if (status !== "done" || !sc.result || !("series" in sc.result)) {
    return (
      <div className="panel grid h-64 place-items-center rounded-xl p-6 text-center text-sm text-muted-foreground">
        {status === "failed" ? (
          <span className="text-crit">Failed: {sc.error ?? update?.error}</span>
        ) : (
          <span className="flex items-center gap-2"><Loader2 className="size-4 animate-spin" />{update?.phase ?? sc.phase ?? "Waiting for a scenario worker…"} ({Math.round((update?.progress ?? sc.progress) * 100)}%)</span>
        )}
      </div>
    );
  }
  const r = sc.result as ScenarioResult;
  const rows = r.series.baseline.map((p, i) => ({ t: tickTime(p.tick), base: p[chart], scen: r.series.scenario[i]?.[chart] ?? null }));
  const chartFmt: Record<typeof chart, (v: number) => string> = {
    congestion: (v) => fmt.pct(v), trip_min_1h: (v) => v.toFixed(1), demand_mw: (v) => v.toFixed(0), pm25: (v) => v.toFixed(1),
  };
  const affected = [...r.roads].sort((a, b) => Math.abs(b.delta) - Math.abs(a.delta)).slice(0, 8);
  const roadName = (id: string) => {
    const x = city.roads.find((y) => y.id === id);
    return x ? `${x.name} · ${x.segment}` : id;
  };
  const districtMetric = sc.kind === "industrial_spike" ? "pm25" : sc.kind === "heatwave" ? "demand" : "none";

  return (
    <motion.div initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} className="space-y-4">
      <div className="panel rounded-xl p-4">
        <div className="flex flex-wrap items-start gap-3">
          <div className="flex-1">
            <div className="eyebrow">{KINDS.find((k) => k.kind === sc.kind)?.title}</div>
            <h2 className="text-lg font-semibold">{sc.name}</h2>
            <p className="font-mono text-[12px] text-muted-foreground">
              simulated {simClock(r.start_time).day} {hhmm(r.start_time)} → {hhmm(r.end_time)} · seed {sc.seed} · spec {sc.spec_hash} · result {r.result_hash} · ran in {(r.fast_forward_s + r.run_s).toFixed(1)} s on {sc.worker}
            </p>
          </div>
        </div>
        <p className="mt-2 rounded-md border border-warn/30 bg-warn/5 px-3 py-1.5 text-[12px] text-warn">
          {r.note} Baseline and scenario start from the identical city state; only the change differs.
        </p>
        <div className="mt-3 grid grid-cols-2 gap-2 2xl:grid-cols-3">
          {METRICS.filter((m) => m.key !== "rerouted" || sc.kind === "road_closure").map((m) => (
            <MetricCard key={m.key} m={m} base={r.baseline[m.key]} scen={r.scenario[m.key]} />
          ))}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <section className="panel rounded-xl p-4">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h3 className="shrink-0 whitespace-nowrap text-[13px] font-medium text-foreground">Baseline vs scenario over time</h3>
            <div className="flex rounded-md border border-border p-0.5 text-[12px]">
              {([["congestion", "Congestion"], ["trip_min_1h", "Journey time"], ["demand_mw", "Demand"], ["pm25", "PM2.5"]] as const).map(([k, l]) => (
                <button key={k} type="button" onClick={() => setChart(k)} className={cn("rounded px-2 py-0.5", chart === k ? "bg-[#2a2a2e] text-foreground" : "text-muted-foreground hover:text-foreground")}>{l}</button>
              ))}
            </div>
          </div>
          <SeriesChart height={230} data={rows} series={[
            { key: "base", label: "Baseline", color: "#8f8f97", type: "line", dashed: true, format: chartFmt[chart] },
            { key: "scen", label: "Scenario", color: "#4c8dff", format: chartFmt[chart] },
          ]} />
          {chart === "trip_min_1h" && <p className="text-[12px] text-muted-foreground">Average duration of trips completed in the preceding hour.</p>}
        </section>
        <section className="panel flex flex-col rounded-xl p-4">
          <h3 className="text-[13px] font-medium text-foreground mb-2 flex items-center gap-1.5"><MapPin className="size-3.5" /> Where it changed — mean congestion over the window</h3>
          <div className="h-[260px] overflow-hidden rounded-lg border border-border">
            <DiffMap city={city} result={r} districtMetric={districtMetric} />
          </div>
          {districtMetric !== "none" && <p className="mt-1 text-[12px] text-muted-foreground">Districts tinted by the change in {districtMetric === "pm25" ? "PM2.5" : "electricity demand"}.</p>}
        </section>
      </div>

      <div className="grid grid-cols-1 gap-4 2xl:grid-cols-2">
        <section className="panel rounded-xl p-4">
          <h3 className="text-[13px] font-medium text-foreground mb-2 flex items-center gap-1.5"><BrainCircuit className="size-3.5 text-muted-foreground" /> What the models forecast next</h3>
          <p className="mb-2 text-[12px] text-muted-foreground">Each branch&apos;s last 24 h of telemetry is sent to the trained models; their forecasts for the period after the window are compared.</p>
          <ForecastTable f={r.forecasts} />
        </section>
        <section className="panel rounded-xl p-4">
          <h3 className="mb-2 text-[13px] font-medium text-foreground">Most affected roads</h3>
          <ul className="space-y-1 text-xs">
            {affected.map((a) => (
              <li key={a.id} className="flex items-center gap-2">
                <span className="flex-1 truncate">{roadName(a.id)}{a.closed && <span className="ml-1 text-[12px] text-crit">CLOSED</span>}</span>
                <span className="font-mono text-muted-foreground num">{fmt.pct(a.baseline)} → {fmt.pct(a.scenario)}</span>
                <span className={cn("w-14 text-right font-mono num", a.delta > 0 ? "text-crit" : "text-ok")}>{a.delta >= 0 ? "+" : ""}{(a.delta * 100).toFixed(1)}</span>
              </li>
            ))}
          </ul>
          {r.context?.nearby_roads && (
            <p className="mt-3 text-[12px] text-muted-foreground">
              PostGIS: {r.context.nearby_roads.length} roads lie within {r.context.radius_m / 1000} km of the closure; their congestion changed by{" "}
              <span className="font-mono text-foreground">{((r.context.nearby_mean_delta ?? 0) * 100).toFixed(1)} pts</span> on average.
            </p>
          )}
          {r.context?.nearby_stations && (
            <p className="mt-3 text-[12px] text-muted-foreground">
              PostGIS: air stations within {r.context.radius_m / 1000} km of the district — {r.context.nearby_stations.map((s) => s.id).join(", ")}.
            </p>
          )}
        </section>
      </div>
    </motion.div>
  );
}

// ------------------------------------------------------------------- page

export function ScenarioLab() {
  const city = useLive((s) => s.city);
  const updates = useLive((s) => s.scenarioUpdates);
  const [items, setItems] = useState<Scenario[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [queue, setQueue] = useState<{ waiting: number; running: number; consumers: number } | null>(null);

  const refresh = useCallback(() => {
    api.scenarios(30).then((list) => {
      setItems(list);
      setError(null);
      setSelected((cur) => cur ?? list.find((s) => s.status === "done")?.id ?? list[0]?.id ?? null);
    }, (e: Error) => setError(e.message));
    api.scenarioQueue().then(setQueue, () => setQueue(null));
  }, []);
  useEffect(refresh, [refresh]);
  // Refresh the list when any scenario finishes.
  const finished = Object.values(updates).filter((u) => u.status === "done" || u.status === "failed").length;
  useEffect(() => {
    if (finished) refresh();
  }, [finished, refresh]);

  if (!city) return <div className="p-6 text-sm text-muted-foreground">Waiting for the live city…</div>;
  return (
    <div className="mx-auto grid max-w-[1600px] grid-cols-1 gap-5 px-4 py-6 sm:px-6 lg:grid-cols-[360px_minmax(0,1fr)]">
      <div className="space-y-4">
        <div>
          <div className="eyebrow flex items-center gap-1.5"><FlaskConical className="size-3.5" /> Scenario lab</div>
          <h1 className="mt-1 text-[22px] font-semibold tracking-[-0.01em]">What if…?</h1>
          {queue && (
            <p className="mt-1.5 text-[12.5px] text-muted-foreground num">
              queue: {queue.waiting} waiting · {queue.running} running · {queue.consumers} worker{queue.consumers === 1 ? "" : "s"}
            </p>
          )}
        </div>
        <Builder city={city} onCreated={(s) => { setItems((xs) => [s, ...(xs ?? [])]); setSelected(s.id); }} />
        <section className="panel rounded-xl p-3">
          <h2 className="text-[13px] font-medium text-foreground mb-2 px-1">Experiments</h2>
          {error ? (
            <p className="p-2 text-xs text-crit">{error}</p>
          ) : items ? (
            <RunList items={items} selected={selected} onSelect={setSelected} />
          ) : (
            <div className="h-24 animate-pulse rounded-lg bg-raised" />
          )}
        </section>
      </div>
      <div>
        {selected ? <ResultView key={selected} city={city} id={selected} /> : (
          <div className="panel grid h-64 place-items-center rounded-xl text-sm text-muted-foreground">Run an experiment to see its result here.</div>
        )}
      </div>
    </div>
  );
}
