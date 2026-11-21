"use client";

import Link from "next/link";
import { memo, useEffect, useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Car,
  Cpu,
  Wind,
  Zap,
} from "lucide-react";
import { Area, AreaChart, Bar, BarChart, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { SeriesChart, type Series } from "@/components/charts/series-chart";
import { axisTick, cursorFill, tooltipStyle } from "@/lib/chart-theme";
import { api } from "@/lib/api";
import { districtTypeColor, fmt, hhmm, pmColor, pmLabel, severityColor, simClock, utilColor } from "@/lib/format";
import { useLive } from "@/lib/store";
import { useHistory } from "@/lib/use-history";
import type { City, RuntimeInfo, Summary, Telemetry } from "@/lib/types";
import { useThrottled } from "@/lib/use-throttled";
import { cn } from "@/lib/utils";
import { Card, Page, PageHeader, Stat, buttonClass } from "@/components/ui/nx";

function Panel({
  title,
  icon,
  children,
  className,
  action,
  flush,
}: {
  title: string;
  icon?: typeof Car;
  children: React.ReactNode;
  className?: string;
  action?: React.ReactNode;
  flush?: boolean;
}) {
  return (
    <Card title={title} icon={icon} action={action} className={className} flush={flush}>
      {children}
    </Card>
  );
}

function Kpi({
  label,
  value,
  unit,
  sub,
  spark,
  sparkKey,
  color = "#4c8dff",
}: {
  label: string;
  value: string;
  unit?: string;
  sub?: React.ReactNode;
  spark: SparkPoint[] | undefined;
  sparkKey: keyof Summary;
  color?: string;
}) {
  return (
    <Stat label={label} value={value} unit={unit} sub={sub}>
      <div className="-mx-4 -mb-3 mt-2 h-9" aria-hidden>
        {spark && spark.length > 1 && <Sparkline data={spark} id={String(sparkKey)} color={color} />}
      </div>
    </Stat>
  );
}

type SparkPoint = { v: number };

/**
 * Memoised so it redraws only when its history changes (every few seconds),
 * not on every telemetry message that updates the KPI value above it.
 */
const Sparkline = memo(function Sparkline({ data, id, color }: { data: SparkPoint[]; id: string; color: string }) {
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id={`k-${id}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.18} />
            <stop offset="100%" stopColor={color} stopOpacity={0} />
          </linearGradient>
        </defs>
        <YAxis hide domain={["dataMin", "dataMax"]} />
        <Area type="monotone" dataKey="v" stroke={color} strokeOpacity={0.9} strokeWidth={1.5} fill={`url(#k-${id})`} isAnimationActive={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
});

const SPARK_KEYS = ["vehicles", "avg_speed_kmh", "avg_congestion", "demand_mw", "avg_pm25", "calls_1h"] as const;
/** A 200 px tile can't show 360 points; ~120 look the same and draw ~3x faster. */
const SPARK_POINTS = 120;

const TRAFFIC_SERIES: Series[] = [
  { key: "cong", label: "Congestion index", color: "#f28c38", format: (v) => fmt.pct(v, 1) },
  { key: "speed", label: "Avg speed (km/h)", color: "#4c8dff", type: "line", axis: "right", format: (v) => v.toFixed(0) },
];
const TRAFFIC_DOMAIN: [number, "auto"] = [0, "auto"];

function RuntimeCard() {
  const [info, setInfo] = useState<RuntimeInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () =>
      api.runtime().then(
        (r) => alive && (setInfo(r), setError(null)),
        (e: Error) => alive && setError(e.message),
      );
    load();
    const t = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);
  if (error) return <p className="text-xs text-crit">{error}</p>;
  if (!info) return <div className="h-24 animate-pulse rounded-lg bg-secondary/40" />;
  const hb = info.simulation.heartbeat ?? {};
  const rows: [string, boolean, string][] = [
    ["Gateway", true, `up ${Math.round(info.gateway.uptime_s / 60)} min · ${info.gateway.ws_clients} WS clients · ${info.gateway.heap_mb.toFixed(1)} MB heap`],
    ["Redis streams", info.redis.ok, info.redis.ok ? `${info.redis.latency_ms.toFixed(1)} ms · ${info.redis.used_memory_mb.toFixed(1)} MB used` : (info.redis.error ?? "down")],
    [
      "Simulation worker",
      info.simulation.alive,
      info.simulation.alive
        ? `${Number(hb.steps_per_sec ?? 0).toFixed(0)} steps/s · ${Number(hb.step_ms ?? 0).toFixed(2)} ms/step · ${hb.agents} agents`
        : "no heartbeat",
    ],
  ];
  return (
    <div className="space-y-2">
      {rows.map(([name, ok, detail]) => (
        <div key={name} className="flex items-center gap-3 text-[13px]">
          <span className={cn("size-2 rounded-full", ok ? "bg-ok" : "bg-crit")} />
          <span className="w-32 shrink-0">{name}</span>
          <span className="truncate text-muted-foreground num">{detail}</span>
        </div>
      ))}
      <p className="pt-1 text-[12px] leading-relaxed text-muted-foreground">
        Mode <span className="text-foreground">{info.mode}</span> — real process status from the gateway and the worker heartbeat. Pods,
        autoscaling and resource use are on the <Link href="/infrastructure" className="text-brand hover:underline">Infrastructure</Link> page.
      </p>
    </div>
  );
}

const EVENT_LABEL: Record<string, string> = {
  "incident.started": "Incident",
  "incident.cleared": "Cleared",
  "emergency.call": "EMS call",
  "sensor.fault": "Sensor",
  "sensor.recovered": "Sensor",
  "alert.opened": "Alert",
  "alert.resolved": "Resolved",
};

/**
 * The two bar charts and the district table only need a ~1 s cadence; redrawing
 * them on every telemetry message is the page's biggest render cost. Memoised
 * and fed the throttled snapshot so the live KPI strip above ticks cheaply.
 */
const HeavyPanels = memo(function HeavyPanels({ t, city }: { t: Telemetry; city: City }) {
  const energyRows = city.districts.map((d, i) => ({
    name: d.id,
    full: d.name,
    demand: t.districts[i].demand_mw,
    headroom: Math.max(0, t.districts[i].capacity_mw - t.districts[i].demand_mw),
    util: t.districts[i].utilization,
  }));
  const stationRows = t.stations.map((st) => {
    const f = city.facilities.find((x) => x.id === st.id);
    return { name: f ? city.districts[f.district].id : st.id, full: f?.name ?? st.id, pm: st.pm25 ?? 0, status: st.status, raw: st.pm25 };
  });

  return (
    <>
      <Panel title="Electricity demand vs substation capacity" icon={Zap} className="xl:col-span-6">
        <div className="h-[230px]">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={energyRows} margin={{ top: 4, right: 4, bottom: 0, left: -18 }}>
              <XAxis dataKey="name" tick={axisTick} axisLine={false} tickLine={false} />
              <YAxis tick={axisTick} axisLine={false} tickLine={false} />
              <Tooltip
                cursor={cursorFill}
                contentStyle={tooltipStyle}
                labelFormatter={(_, p) => (p?.[0]?.payload as { full?: string })?.full ?? ""}
                formatter={(v, n) => [`${Number(v).toFixed(1)} MW`, n === "demand" ? "Demand" : "Headroom"]}
              />
              <Bar dataKey="demand" stackId="e" radius={[0, 0, 2, 2]} isAnimationActive={false}>
                {energyRows.map((r) => (
                  <Cell key={r.name} fill={utilColor(r.util)} fillOpacity={0.85} />
                ))}
              </Bar>
              <Bar dataKey="headroom" stackId="e" fill="rgb(255 255 255 / 0.05)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
            </BarChart>
          </ResponsiveContainer>
        </div>
        <p className="mt-2 text-[12px] text-muted-foreground">Full bar height = substation capacity. Colour shows load: green &lt; 75%, amber &lt; 90%, red ≥ 90%.</p>
      </Panel>

      <Panel title="Air-quality stations — PM2.5" icon={Wind} className="xl:col-span-6">
        <div className="h-[230px]">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={stationRows} margin={{ top: 4, right: 4, bottom: 0, left: -18 }}>
              <XAxis dataKey="name" tick={axisTick} axisLine={false} tickLine={false} />
              <YAxis tick={axisTick} axisLine={false} tickLine={false} />
              <Tooltip
                cursor={cursorFill}
                contentStyle={tooltipStyle}
                labelFormatter={(_, p) => (p?.[0]?.payload as { full?: string })?.full ?? ""}
                formatter={(_, __, item) => {
                  const r = item.payload as { raw: number | null; status: string };
                  return [r.raw == null ? "offline" : `${r.raw.toFixed(1)} µg/m³${r.status === "stuck" ? " (stuck sensor)" : ""}`, "PM2.5"];
                }}
              />
              <Bar dataKey="pm" radius={[2, 2, 0, 0]} isAnimationActive={false}>
                {stationRows.map((r) => (
                  <Cell key={r.name} fill={r.status === "ok" ? pmColor(r.raw) : "#52525b"} fillOpacity={r.status === "ok" ? 0.85 : 0.5} />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>
        <p className="mt-2 text-[12px] text-muted-foreground">
          Synthetic dispersion-model output, coloured on the US EPA scale for orientation only. Grey = faulty sensor
          {t.summary.sensor_faults ? ` (${t.summary.sensor_faults} now)` : ""}.
        </p>
      </Panel>

      <Panel title="Districts" icon={Activity} className="xl:col-span-8" flush>
        <div className="overflow-x-auto">
          <table className="w-full text-[13px]">
            <thead>
              <tr className="border-b border-border text-left text-muted-foreground">
                {["District", "Type", "Vehicles", "Grid load", "PM2.5", "EMS 1h"].map((h) => (
                  <th key={h} className="h-9 px-4 text-[12px] font-medium">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {city.districts.map((d, i) => {
                const o = t.districts[i];
                return (
                  <tr key={d.id} className="border-b border-border last:border-0 hover:bg-raised">
                    <td className="h-10 px-4">
                      <span className="mr-1.5 text-muted-foreground num">{d.id}</span>
                      {d.name}
                    </td>
                    <td className="px-4">
                      <span className="flex items-center gap-1.5 capitalize text-muted-foreground">
                        <span className="size-2 rounded-full" style={{ background: districtTypeColor[d.type] }} />
                        {d.type}
                      </span>
                    </td>
                    <td className="px-4 num">{fmt.int(o.vehicles)}</td>
                    <td className="px-4">
                      <div className="flex items-center gap-2">
                        <div className="h-1.5 w-20 overflow-hidden rounded-full bg-raised">
                          <div className="h-full rounded-full" style={{ width: `${Math.min(100, o.utilization * 100)}%`, background: utilColor(o.utilization) }} />
                        </div>
                        <span className="w-9 num">{fmt.pct(o.utilization)}</span>
                      </div>
                    </td>
                    <td className="px-4 num" style={{ color: pmColor(o.pm25) }}>
                      {fmt.num(o.pm25)}
                    </td>
                    <td className="px-4 num">
                      {o.calls_1h}
                      {o.calls_1h > 0 && <span className="text-muted-foreground"> · {fmt.num(o.avg_eta_min)}m</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Panel>
    </>
  );
});

export function Overview() {
  const t = useLive((s) => s.telemetry);
  const city = useLive((s) => s.city);
  const events = useLive((s) => s.events);
  const conn = useLive((s) => s.conn);
  // 5 s: at 60x a fresh 360-point history every few seconds is plenty for
  // 6-hour charts; the KPI values themselves update on every telemetry message.
  const { points } = useHistory<Summary>("city", "nexus", 360, 5000);
  // Bar charts and the district table redraw on this ~1 s snapshot; the KPI
  // strip above stays on live `t` so numbers tick with every telemetry message.
  const chartT = useThrottled(t, 1000);
  const sparks = useMemo(() => {
    if (!points) return null;
    const step = Math.max(1, Math.ceil(points.length / SPARK_POINTS));
    const thin = points.filter((_, i) => i % step === 0 || i === points.length - 1);
    return Object.fromEntries(SPARK_KEYS.map((k) => [k, thin.map((p) => ({ v: p.v[k] as number }))])) as Record<
      (typeof SPARK_KEYS)[number],
      SparkPoint[]
    >;
  }, [points]);
  // One point per 2 simulated minutes (180 for 6 h) is indistinguishable at
  // chart width and halves the redraw cost.
  const trafficData = useMemo(
    () =>
      points
        ?.filter((_, i) => i % 2 === (points.length - 1) % 2)
        .map((p) => ({ t: p.t, cong: p.v.avg_congestion, speed: p.v.avg_speed_kmh })) ?? null,
    [points],
  );

  if (!t || !city) {
    return (
      <div className="grid h-full place-items-center p-6">
        <div className="panel max-w-md rounded-xl p-6 text-center text-[13.5px] text-muted-foreground">
          {conn === "closed"
            ? "The gateway is offline. Start Redis, the simulation worker and the gateway (see README), and this page will connect automatically."
            : "Waiting for the first telemetry from the simulation…"}
        </div>
      </div>
    );
  }

  const s = t.summary;
  const c = simClock(t.sim_time);
  const alerts = t.alerts ?? [];

  return (
    <Page wide>
      <PageHeader
        title="Overview"
        description="Live state of the simulated city: traffic, electricity, air quality and emergency response."
        meta={
          <>
            <span className="num">
              {c.day} {c.date} · {c.time}
            </span>
            <span className="num">
              {t.weather.temp_c.toFixed(1)} °C · {t.weather.humidity.toFixed(0)}% humidity · wind {t.weather.wind_speed_ms.toFixed(1)} m/s
              from {t.weather.wind_dir_deg.toFixed(0)}°
            </span>
          </>
        }
        actions={
          <Link href="/twin" className={buttonClass("secondary")}>
            Open Live Twin <ArrowUpRight className="size-3.5" />
          </Link>
        }
      />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Kpi label="Vehicles on network" value={fmt.int(s.vehicles)} sub={`${fmt.int(s.trips_1h)} trips / last hour`} spark={sparks?.vehicles} sparkKey="vehicles" />
        <Kpi label="Network speed" value={fmt.num(s.avg_speed_kmh, 0)} unit="km/h" sub={s.avg_trip_min ? `avg trip ${fmt.num(s.avg_trip_min)} min` : "—"} spark={sparks?.avg_speed_kmh} sparkKey="avg_speed_kmh" color="#4c8dff" />
        <Kpi label="Congestion index" value={fmt.pct(s.avg_congestion)} sub={`${s.incidents} active incident${s.incidents === 1 ? "" : "s"}`} spark={sparks?.avg_congestion} sparkKey="avg_congestion" color="#f28c38" />
        <Kpi label="Grid demand" value={fmt.int(s.demand_mw)} unit="MW" sub={`${fmt.pct(s.demand_mw / s.capacity_mw)} of ${fmt.int(s.capacity_mw)} MW`} spark={sparks?.demand_mw} sparkKey="demand_mw" color="#f2c037" />
        <Kpi label="PM2.5 (station avg)" value={fmt.num(s.avg_pm25)} unit="µg/m³" sub={<span style={{ color: pmColor(s.max_pm25) }}>max {fmt.num(s.max_pm25)} · {pmLabel(s.max_pm25)}</span>} spark={sparks?.avg_pm25} sparkKey="avg_pm25" color={pmColor(s.avg_pm25)} />
        <Kpi label="EMS calls (1h)" value={String(s.calls_1h)} sub={s.calls_1h ? `avg response ${fmt.num(s.avg_eta_min)} min` : "—"} spark={sparks?.calls_1h} sparkKey="calls_1h" color="#f07a95" />
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-12">
        <Panel title="Traffic — last 6 simulated hours" icon={Activity} className="xl:col-span-8">
          {trafficData && trafficData.length > 1 ? (
            <SeriesChart height={220} data={trafficData} leftDomain={TRAFFIC_DOMAIN} series={TRAFFIC_SERIES} />
          ) : (
            <div className="grid h-[220px] place-items-center text-xs text-muted-foreground">Collecting history…</div>
          )}
        </Panel>
        <Panel
          title={`Active alerts (${alerts.length})`}
          icon={AlertTriangle}
          className="xl:col-span-4"
          action={<span className="text-[12px] text-muted-foreground">Rule-based</span>}
          flush
        >
          {alerts.length === 0 ? (
            <p className="p-4 text-[13px] text-muted-foreground">No rule thresholds are currently breached.</p>
          ) : (
            <ul className="max-h-[252px] divide-y divide-border overflow-y-auto">
              {[...alerts].reverse().map((a) => (
                <li key={a.id} className="px-4 py-2.5 hover:bg-raised">
                  <div className="flex items-center gap-2 text-[13px]">
                    <span className="size-2 shrink-0 rounded-full" style={{ background: severityColor[a.severity] }} />
                    <span className="flex-1 truncate font-medium">{a.title}</span>
                    <span className="text-[12px] text-muted-foreground num">{hhmm(a.opened_at)}</span>
                  </div>
                  <div className="mt-0.5 truncate pl-4 text-[12px] text-muted-foreground">{a.detail}</div>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        {/* `?? t` is only a fallback for the type: chartT initialized from t, so it is never null here. */}
        <HeavyPanels t={chartT ?? t} city={city} />

        <div className="space-y-4 xl:col-span-4">
          <Panel title="Event feed" icon={Activity} flush>
            {events.length === 0 ? (
              <p className="p-4 text-[13px] text-muted-foreground">No events yet in this run.</p>
            ) : (
              <ul className="max-h-[300px] divide-y divide-border overflow-y-auto">
                {events.slice(0, 60).map((e) => (
                  // Stable key (no index): a new event adds one row instead of re-creating all of them.
                  <li key={`${e.tick}|${e.payload.kind}|${e.payload.entity_id}|${e.payload.message}`} className="flex gap-3 px-4 py-2 text-[12.5px] leading-snug">
                    <span className="w-10 shrink-0 text-muted-foreground num">{hhmm(e.sim_time)}</span>
                    <span className="flex w-[72px] shrink-0 items-center gap-1.5 font-medium text-muted-foreground">
                      <span className="size-1.5 rounded-full" style={{ background: severityColor[e.payload.severity] }} />
                      {EVENT_LABEL[e.payload.kind] ?? e.payload.kind}
                    </span>
                    <span className="min-w-0 text-foreground/90">{e.payload.message}</span>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
          <Panel title="Runtime" icon={Cpu}>
            <RuntimeCard />
          </Panel>
        </div>
      </div>
    </Page>
  );
}
