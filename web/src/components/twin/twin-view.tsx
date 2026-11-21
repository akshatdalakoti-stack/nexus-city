"use client";

import dynamic from "next/dynamic";
import { Suspense, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { AnimatePresence, motion } from "motion/react";
import { AlertTriangle, BrainCircuit, Building2, Car, Gauge, Layers, Radio, ScanSearch, Wind, Zap } from "lucide-react";
import { Switch } from "@/components/ui/switch";
import { congestionColor, congestionStops, fmt, pmStops, severityColor } from "@/lib/format";
import { useLive } from "@/lib/store";
import type { Selection, SelectionKind, Telemetry } from "@/lib/types";
import type { LayerToggles } from "@/components/map/city-map";
import { Inspector } from "./inspector";
import { ReplayBar } from "./replay-bar";

const CityMap = dynamic(() => import("@/components/map/city-map").then((m) => m.CityMap), {
  ssr: false,
  loading: () => <MapMessage text="Loading map engine…" />,
});

function MapMessage({ text, tone = "muted" }: { text: string; tone?: "muted" | "error" }) {
  return (
    <div className="absolute inset-0 grid place-items-center">
      <div className={`panel rounded-xl px-5 py-4 text-sm ${tone === "error" ? "text-crit" : "text-muted-foreground"}`}>{text}</div>
    </div>
  );
}

const LAYERS: { key: keyof LayerToggles; label: string; icon: typeof Car }[] = [
  { key: "traffic", label: "Traffic flow", icon: Gauge },
  { key: "vehicles", label: "Vehicles", icon: Car },
  { key: "air", label: "PM2.5 field", icon: Wind },
  { key: "energy", label: "Grid load", icon: Zap },
  { key: "infrastructure", label: "Infrastructure", icon: Building2 },
  { key: "alerts", label: "Alerts", icon: AlertTriangle },
  { key: "forecast", label: "Forecast +30 min", icon: BrainCircuit },
  { key: "anomalies", label: "Model anomalies", icon: ScanSearch },
];

const ALERT_KIND: Record<string, SelectionKind> = {
  road: "road",
  station: "station",
  district: "district",
  hospital: "hospital",
  node: "node",
};

function Legend({ layers }: { layers: LayerToggles }) {
  const gradient = (stops: [number, string][]) => `linear-gradient(90deg, ${stops.map(([, c]) => c).join(", ")})`;
  return (
    <div className="panel w-56 space-y-3 rounded-xl p-3 text-[12px]">
      {layers.traffic && (
        <div>
          <div className="label-caps mb-1">{layers.forecast ? "Congestion forecast +30 min" : "Congestion"}</div>
          <div className="h-1.5 rounded-full" style={{ background: gradient(congestionStops) }} />
          <div className="mt-1 flex justify-between font-mono text-muted-foreground">
            <span>free</span>
            <span>jammed</span>
          </div>
        </div>
      )}
      {layers.air && (
        <div>
          <div className="label-caps mb-1">PM2.5 µg/m³ (synthetic)</div>
          <div className="h-1.5 rounded-full" style={{ background: gradient(pmStops.map(([v, c]) => [v, c])) }} />
          <div className="mt-1 flex justify-between font-mono text-muted-foreground">
            <span>0</span>
            <span>12</span>
            <span>35</span>
            <span>55</span>
            <span>150</span>
          </div>
        </div>
      )}
      {layers.infrastructure && (
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground">
          {[
            ["#f07a95", "Hospital"],
            ["#a78bfa", "School"],
            ["#3ecf8e", "Air station"],
            ["#f2c037", "Substation"],
          ].map(([c, l]) => (
            <span key={l} className="flex items-center gap-1.5">
              <span className="size-2 rounded-full border-2" style={{ borderColor: c }} />
              {l}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function CityGlance({ telemetry, onSelect }: { telemetry: Telemetry | null; onSelect: (s: Selection) => void }) {
  const city = useLive((s) => s.city);
  const top = useMemo(() => {
    if (!telemetry || !city) return [];
    return telemetry.roads
      .map((o, i) => ({ o, r: city.roads[i] }))
      .filter((x) => x.r.class !== "local")
      .sort((a, b) => b.o.congestion - a.o.congestion)
      .slice(0, 6);
  }, [telemetry, city]);
  if (!telemetry) return <p className="p-4 text-sm text-muted-foreground">Waiting for the first telemetry from the simulation…</p>;
  const s = telemetry.summary;
  const alerts = telemetry.alerts ?? [];
  return (
    <div className="space-y-5 p-4">
      <div>
        <h2 className="text-[15px] font-semibold">City at a glance</h2>
        <p className="mt-1 text-xs text-muted-foreground">Click any road, intersection, district or facility on the map to inspect it.</p>
      </div>
      <div className="grid grid-cols-2 gap-2 text-xs">
        {[
          ["Vehicles", fmt.int(s.vehicles)],
          ["Avg speed", `${fmt.num(s.avg_speed_kmh, 0)} km/h`],
          ["Grid demand", `${fmt.int(s.demand_mw)} MW`],
          ["Avg PM2.5", `${fmt.num(s.avg_pm25)} µg/m³`],
        ].map(([k, v]) => (
          <div key={k} className="rounded-lg border border-border bg-raised px-3 py-2">
            <div className="label-caps">{k}</div>
            <div className="mt-0.5 font-mono text-[15px] num">{v}</div>
          </div>
        ))}
      </div>
      <section>
        <h3 className="mb-2 text-[13px] font-medium text-foreground">Most congested major roads</h3>
        <ul className="space-y-1">
          {top.map(({ o, r }) => (
            <li key={r.id}>
              <button
                type="button"
                onClick={() => onSelect({ kind: "road", id: r.id })}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent"
              >
                <span className="h-1.5 w-8 rounded-full" style={{ background: congestionColor(o.congestion) }} />
                <span className="flex-1 truncate">
                  {r.name} <span className="text-muted-foreground">· {r.segment}</span>
                </span>
                <span className="font-mono text-muted-foreground num">{fmt.pct(o.congestion)}</span>
              </button>
            </li>
          ))}
        </ul>
      </section>
      <section>
        <h3 className="mb-2 text-[13px] font-medium text-foreground">Active alerts ({alerts.length})</h3>
        {alerts.length === 0 ? (
          <p className="text-xs text-muted-foreground">No rule thresholds are currently breached.</p>
        ) : (
          <ul className="space-y-1">
            {alerts.slice(0, 12).map((a) => (
              <li key={a.id}>
                <button
                  type="button"
                  onClick={() => ALERT_KIND[a.entity_type] && onSelect({ kind: ALERT_KIND[a.entity_type], id: a.entity_id })}
                  className="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent"
                >
                  <span className="mt-1 size-1.5 shrink-0 rounded-full" style={{ background: severityColor[a.severity] }} />
                  <span>{a.title}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

/** A new simulation run remounts the view, clearing selection and replay. */
export function TwinView() {
  return (
    <Suspense>
      {/* The map is the page's content, so its heading is for screen readers only. */}
      <h1 className="sr-only">Live Twin: map of Nexus City</h1>
      <TwinViewKeyed />
    </Suspense>
  );
}

const SELECTABLE = ["road", "node", "district", "station", "hospital", "school", "substation"];

/** Reads ?select=kind:id (links from the Predictions page) and remounts per run. */
function TwinViewKeyed() {
  const runId = useLive((s) => s.runId);
  const params = useSearchParams();
  const [kind, id] = (params.get("select") ?? "").split(":");
  const initial = SELECTABLE.includes(kind) && id ? ({ kind, id } as Selection) : null;
  return <TwinViewForRun key={runId ?? "none"} initialSelection={initial} />;
}

function TwinViewForRun({ initialSelection }: { initialSelection: Selection | null }) {
  const city = useLive((s) => s.city);
  const cityError = useLive((s) => s.cityError);
  const conn = useLive((s) => s.conn);
  const liveTelemetry = useLive((s) => s.telemetry);
  const [layers, setLayers] = useState<LayerToggles>({
    traffic: true,
    vehicles: true,
    air: true,
    energy: false,
    infrastructure: true,
    alerts: true,
    forecast: false,
    anomalies: true,
  });
  const [selection, setSelection] = useState<Selection | null>(initialSelection);
  const traffic = useLive((s) => s.forecasts.traffic);
  const anomalies = useLive((s) => s.anomalies);
  const roadForecast = useMemo(() => {
    const f = traffic?.payload;
    return f ? new Map(f.entities.map((id, i) => [id, f.values["30"][i]])) : null;
  }, [traffic]);
  const [replay, setReplay] = useState<{ at: string; telemetry: Telemetry | null } | null>(null);

  const telemetry = replay ? replay.telemetry : liveTelemetry;

  return (
    <div className="relative h-full overflow-hidden">
      {city ? (
        <CityMap
          city={city}
          telemetry={telemetry}
          layers={layers}
          selection={selection}
          onSelect={setSelection}
          live={!replay}
          roadForecast={roadForecast}
          modelAnomalies={anomalies?.payload.flagged}
        />
      ) : cityError ? (
        <MapMessage tone="error" text={`Could not load the city: ${cityError}`} />
      ) : (
        <MapMessage text={conn === "closed" ? "Gateway offline — start the gateway and simulation worker (see README)." : "Loading Nexus City…"} />
      )}

      {/* Layer controls */}
      <div className="pointer-events-none absolute left-4 top-4 flex flex-col gap-3">
        <div className="panel pointer-events-auto w-56 rounded-xl p-3">
          <div className="label-caps mb-2 flex items-center gap-1.5">
            <Layers className="size-3.5" /> Layers
          </div>
          <ul className="space-y-1.5">
            {LAYERS.map(({ key, label, icon: Icon }) => {
              const disabled = (key === "vehicles" || key === "forecast" || key === "anomalies") && !!replay;
              return (
                <li key={key} className="flex items-center gap-2 text-xs">
                  <Icon className="size-3.5 text-muted-foreground" />
                  <label htmlFor={`layer-${key}`} className={`flex-1 ${disabled ? "text-muted-foreground/50" : ""}`}>
                    {label}
                    {disabled && <span className="ml-1 text-[12px]">(live only)</span>}
                  </label>
                  <Switch
                    id={`layer-${key}`}
                    size="sm"
                    checked={layers[key]}
                    disabled={disabled}
                    onCheckedChange={(v: boolean) => setLayers((l) => ({ ...l, [key]: v }))}
                  />
                </li>
              );
            })}
          </ul>
        </div>
        <div className="pointer-events-auto">
          <Legend layers={layers} />
        </div>
      </div>

      {/* Replay indicator */}
      {replay && (
        <div className="absolute left-[260px] top-4 flex items-center gap-2 rounded-full border border-warn/40 bg-warn/10 px-3 py-1 font-mono text-[12px] text-warn">
          <Radio className="size-3.5" /> REPLAY · vehicles not recorded
        </div>
      )}

      {/* Inspector */}
      <aside className="panel absolute bottom-24 right-4 top-4 w-[340px] overflow-hidden rounded-xl">
        <AnimatePresence mode="wait" initial={false}>
          <motion.div
            key={selection ? `${selection.kind}:${selection.id}` : "glance"}
            initial={{ opacity: 0, x: 12 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -8 }}
            transition={{ duration: 0.18 }}
            className="h-full overflow-y-auto"
          >
            {selection && city ? (
              <Inspector city={city} telemetry={telemetry} selection={selection} onClose={() => setSelection(null)} marker={replay?.at ?? null} />
            ) : (
              <CityGlance telemetry={telemetry} onSelect={setSelection} />
            )}
          </motion.div>
        </AnimatePresence>
      </aside>

      <ReplayBar replay={replay} onChange={setReplay} />
    </div>
  );
}
