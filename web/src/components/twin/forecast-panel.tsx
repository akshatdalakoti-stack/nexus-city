"use client";

import { useEffect, useState } from "react";
import { BrainCircuit, ScanSearch } from "lucide-react";
import { SeriesChart } from "@/components/charts/series-chart";
import { api } from "@/lib/api";
import { anomalyFor, forecastFor, horizonLabel } from "@/lib/forecast";
import { congestionColor, fmt, pmColor, utilColor } from "@/lib/format";
import { useLive } from "@/lib/store";
import type { City, ForecastModelKind, ForecastPoint, HistoryPoint } from "@/lib/types";
import { useHistory } from "@/lib/use-history";

interface Props {
  city: City;
  kind: "road" | "district" | "station";
  id: string;
}

const MODEL_OF: Record<Props["kind"], ForecastModelKind> = { road: "traffic", district: "energy", station: "pm25" };

/** Forecasts and anomaly status for one entity, from the live model services. */
export function ForecastPanel({ city, kind, id }: Props) {
  const model = MODEL_OF[kind];
  const env = useLive((s) => s.forecasts[model]);
  const anomalies = useLive((s) => s.anomalies);
  const telemetry = useLive((s) => s.telemetry);
  const values = forecastFor(env, id);
  const flagged = anomalyFor(anomalies, id);

  return (
    <section className="space-y-2">
      <h3 className="flex items-center gap-1.5 text-[13px] font-medium text-foreground">
        <BrainCircuit className="size-3.5 text-muted-foreground" /> Model forecast
      </h3>
      {!env || !values ? (
        <p className="rounded-lg border border-dashed border-border p-3 text-xs leading-relaxed text-muted-foreground">
          No forecast available — the {model} model service is not running or has not produced a forecast for this run yet.
          Start it with <code className="font-mono">nexus-ml serve</code> (see README).
        </p>
      ) : (
        <>
          {kind === "road" && <RoadForecast id={id} values={values} unit={env.payload.horizon_unit} />}
          {kind === "station" && <StationForecast id={id} values={values} unit={env.payload.horizon_unit} />}
          {kind === "district" && (
            <DistrictForecast
              values={values}
              issuedTick={env.payload.issued_tick}
              capacity={city.districts.find((d) => d.id === id)?.substation_mw ?? 0}
              now={telemetry?.districts.find((d) => d.id === id)?.demand_mw ?? null}
            />
          )}
          <p className="text-[12px] text-muted-foreground">
            <span className="font-mono">{env.payload.version}</span> · issued{" "}
            {fmt.int(Math.max(0, ((telemetry?.tick ?? env.payload.issued_tick) - env.payload.issued_tick) / 60))} sim-min ago ·
            trained on other simulated cities; synthetic, not a real-world forecast.
          </p>
        </>
      )}
      <div className="flex items-start gap-2 rounded-lg border border-border bg-raised px-3 py-2 text-xs">
        <ScanSearch className="mt-0.5 size-3.5 shrink-0 text-violet-400" />
        {anomalies ? (
          flagged ? (
            <span>
              <span className="font-medium text-violet-300">Anomaly detector flags this {kind}</span> — {flagged.reason}. Score{" "}
              {flagged.score.toFixed(3)}.
            </span>
          ) : (
            <span className="text-muted-foreground">Anomaly detector: nothing unusual.</span>
          )
        ) : (
          <span className="text-muted-foreground">Anomaly detector not running.</span>
        )}
      </div>
    </section>
  );
}

function Chips({ values, unit, color, format }: { values: Map<number, number>; unit: string; color: (v: number) => string; format: (v: number) => string }) {
  return (
    <div className="grid grid-cols-3 gap-2">
      {[...values].map(([h, v]) => (
        <div key={h} className="rounded-lg border border-border bg-raised px-2.5 py-2">
          <div className="label-caps">{horizonLabel(h, unit)}</div>
          <div className="mt-0.5 font-mono text-base num" style={{ color: color(v) }}>
            {format(v)}
          </div>
        </div>
      ))}
    </div>
  );
}

/** Actual history plus the past forecasts (at one horizon) for the same entity. */
function useOverlay<T>(model: ForecastModelKind, historyKind: string, id: string, horizon: number) {
  const seq = useLive((s) => s.telemetrySeq);
  const { points } = useHistory<T>(historyKind, id, 180);
  const [past, setPast] = useState<ForecastPoint[]>([]);
  useEffect(() => {
    let alive = true;
    api.entityForecasts(model, id, 240).then(
      (r) => alive && setPast(r.points.filter((p) => p.horizon === horizon)),
      () => alive && setPast([]),
    );
    return () => {
      alive = false;
    };
    // refresh with telemetry, at most as often as history refreshes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [model, id, horizon, Math.floor(seq / 3)]);
  return { points, past };
}

function epochPlus(tick: number) {
  return new Date(Date.UTC(2026, 5, 1, 5, 30) + tick * 1000).toISOString();
}

/** Chart rows: actual values, past forecasts at their target times, and the forecast ahead. */
function mergeRows<T>(actual: HistoryPoint<T>[] | null, pick: (v: T) => number | null, past: ForecastPoint[], ahead: [string, number][]) {
  const rows = new Map<string, Record<string, number | string | null>>();
  for (const p of actual ?? []) rows.set(p.t, { t: p.t, actual: pick(p.v) });
  const firstActual = actual?.[0]?.t;
  for (const p of past) {
    const t = epochPlus(p.target_tick).replace(".000Z", "Z");
    if (firstActual && t < firstActual) continue;
    rows.set(t, { ...(rows.get(t) ?? { t }), past: p.value });
  }
  for (const [t, v] of ahead) rows.set(t, { ...(rows.get(t) ?? { t }), ahead: v });
  return [...rows.values()].sort((a, b) => String(a.t).localeCompare(String(b.t)));
}

function RoadForecast({ id, values, unit }: { id: string; values: Map<number, number>; unit: string }) {
  const issued = useLive((s) => s.forecasts.traffic?.payload.issued_tick ?? 0);
  const now = useLive((s) => s.telemetry?.roads.find((r) => r.id === id)?.congestion ?? null);
  const { points, past } = useOverlay<{ congestion: number }>("traffic", "road", id, 30);
  const ahead: [string, number][] = [...values].map(([h, v]) => [epochPlus(issued + h * 60).replace(".000Z", "Z"), v]);
  if (now !== null) ahead.unshift([epochPlus(issued).replace(".000Z", "Z"), now]);
  const rows = mergeRows(points, (v) => v.congestion, past, ahead);
  return (
    <>
      <Chips values={values} unit={unit} color={congestionColor} format={(v) => fmt.pct(v)} />
      {rows.length > 2 && (
        <SeriesChart
          height={140}
          data={rows}
          leftDomain={[0, 1]}
          series={[
            { key: "actual", label: "Actual", color: "#f28c38", format: (v) => fmt.pct(v) },
            { key: "past", label: "Forecast made 30 min earlier", color: "#a78bfa", type: "line", dashed: true, format: (v) => fmt.pct(v) },
            { key: "ahead", label: "Forecast ahead", color: "#4c8dff", type: "line", format: (v) => fmt.pct(v) },
          ]}
        />
      )}
    </>
  );
}

function StationForecast({ id, values, unit }: { id: string; values: Map<number, number>; unit: string }) {
  const issued = useLive((s) => s.forecasts.pm25?.payload.issued_tick ?? 0);
  const { points, past } = useOverlay<{ pm25: number | null; status: string }>("pm25", "station", id, 30);
  const ahead: [string, number][] = [...values].map(([h, v]) => [epochPlus(issued + h * 60).replace(".000Z", "Z"), v]);
  const rows = mergeRows(points, (v) => (v.status === "ok" ? v.pm25 : null), past, ahead);
  return (
    <>
      <div className="grid grid-cols-2 gap-2">
        <Chips values={values} unit={unit} color={pmColor} format={(v) => `${fmt.num(v)} µg/m³`} />
      </div>
      {rows.length > 2 && (
        <SeriesChart
          height={140}
          data={rows}
          series={[
            { key: "actual", label: "Actual", color: "#f28c38", format: (v) => v.toFixed(1) },
            { key: "past", label: "Forecast made 30 min earlier", color: "#a78bfa", type: "line", dashed: true, format: (v) => v.toFixed(1) },
            { key: "ahead", label: "Forecast ahead", color: "#4c8dff", type: "line", format: (v) => v.toFixed(1) },
          ]}
        />
      )}
    </>
  );
}

function DistrictForecast({ values, issuedTick, capacity, now }: { values: Map<number, number>; issuedTick: number; capacity: number; now: number | null }) {
  const rows = [...values].map(([h, v]) => ({ t: epochPlus(issuedTick + h * 3600).replace(".000Z", "Z"), demand: v, cap: capacity }));
  if (now !== null) rows.unshift({ t: epochPlus(issuedTick).replace(".000Z", "Z"), demand: now, cap: capacity });
  const peak = [...values].reduce((a, b) => (b[1] > a[1] ? b : a));
  const util = peak[1] / capacity;
  return (
    <>
      <p className="text-xs">
        Next 24 h peak: <span className="font-mono num" style={{ color: utilColor(util) }}>{fmt.num(peak[1])} MW</span> in {peak[0]} h
        ({fmt.pct(util)} of the {capacity} MW substation).
      </p>
      <SeriesChart
        height={140}
        data={rows}
        series={[
          { key: "demand", label: "Forecast demand", color: "#4c8dff", format: (v) => `${v.toFixed(0)}` },
          { key: "cap", label: "Capacity", color: "#f0565b", type: "line", dashed: true, format: (v) => `${v.toFixed(0)}` },
        ]}
      />
    </>
  );
}
