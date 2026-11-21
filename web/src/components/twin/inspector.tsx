"use client";

import { X } from "lucide-react";
import { SeriesChart, type Series } from "@/components/charts/series-chart";
import { ForecastPanel } from "./forecast-panel";
import { congestionColor, fmt, pmColor, pmLabel, severityColor, utilColor } from "@/lib/format";
import { useHistory } from "@/lib/use-history";
import type {
  Alert,
  City,
  DistrictObs,
  HospitalObs,
  NodeObs,
  RoadObs,
  Selection,
  StationObs,
  Telemetry,
} from "@/lib/types";

interface Props {
  city: City;
  telemetry: Telemetry | null;
  selection: Selection;
  onClose: () => void;
  marker: string | null;
}

function Stat({ label, value, unit, color }: { label: string; value: string; unit?: string; color?: string }) {
  return (
    <div className="rounded-lg border border-border bg-raised px-3 py-2">
      <div className="label-caps">{label}</div>
      <div className="mt-0.5 font-mono text-lg num" style={{ color }}>
        {value}
        {unit && <span className="ml-1 text-xs text-muted-foreground">{unit}</span>}
      </div>
    </div>
  );
}

function Attr({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="flex justify-between gap-3 border-b border-border/50 py-1.5 text-xs last:border-0">
      <span className="text-muted-foreground">{k}</span>
      <span className="text-right font-mono text-foreground/90">{v}</span>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-[13px] font-medium text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function History<T>({
  kind,
  id,
  series,
  map,
  marker,
  leftDomain,
}: {
  kind: string;
  id: string;
  series: Series[];
  map: (v: T) => Record<string, number | null>;
  marker: string | null;
  leftDomain?: [number | "auto", number | "auto"];
}) {
  const { points, error, loading } = useHistory<T>(kind, id, 180);
  if (error) return <p className="text-xs text-crit">History unavailable: {error}</p>;
  if (loading || !points) return <div className="h-[150px] animate-pulse rounded-lg bg-raised" />;
  if (points.length < 2)
    return <p className="text-xs text-muted-foreground">Collecting history — at least two simulated minutes are needed.</p>;
  return (
    <SeriesChart data={points.map((p) => ({ t: p.t, ...map(p.v) }))} series={series} marker={marker} leftDomain={leftDomain} />
  );
}

function RelatedAlerts({ alerts }: { alerts: Alert[] }) {
  if (alerts.length === 0) return null;
  return (
    <Section title="Active alerts">
      <ul className="space-y-1.5">
        {alerts.map((a) => (
          <li key={a.id} className="rounded-md border border-border bg-raised px-2.5 py-1.5 text-xs">
            <div className="flex items-center gap-2">
              <span className="size-1.5 rounded-full" style={{ background: severityColor[a.severity] }} />
              <span className="font-medium">{a.title}</span>
            </div>
            <div className="mt-0.5 text-muted-foreground">{a.detail}</div>
          </li>
        ))}
      </ul>
    </Section>
  );
}

export function Inspector({ city, telemetry, selection, onClose, marker }: Props) {
  const { kind, id } = selection;
  // Substations and schools share their district's alerts.
  const facility = kind === "substation" || kind === "school" ? city.facilities.find((f) => f.id === id) : undefined;
  const related = new Set([id, ...(facility ? [city.districts[facility.district].id] : [])]);
  const alerts = (telemetry?.alerts ?? []).filter((a) => related.has(a.entity_id));
  let title = id;
  let subtitle = "";
  let body: React.ReactNode = null;

  if (kind === "road") {
    const i = city.roads.findIndex((r) => r.id === id);
    const r = city.roads[i];
    const o = telemetry?.roads[i];
    if (r) {
      title = r.name;
      subtitle = `${r.segment} · ${r.class}`;
      body = (
        <>
          {o && (
            <div className="grid grid-cols-2 gap-2">
              <Stat label="Speed" value={fmt.num(o.speed_kmh, 0)} unit={`/ ${r.speed_kmh} km/h`} />
              <Stat label="Congestion" value={fmt.pct(o.congestion)} color={congestionColor(o.congestion)} />
              <Stat label="Flow" value={fmt.int(o.flow_vph)} unit="veh/h" />
              <Stat label="On segment" value={fmt.int(o.vehicles)} unit="veh" />
            </div>
          )}
          {o?.incident && (
            <p className="rounded-md border border-crit/40 bg-crit/10 px-2.5 py-1.5 text-xs text-crit">
              Incident {o.incident}: lanes reduced on this segment.
            </p>
          )}
          <Section title="Last 3 simulated hours">
            <History<RoadObs>
              kind="road"
              id={id}
              marker={marker}
              leftDomain={[0, 1]}
              map={(v) => ({ cong: v.congestion, speed: v.speed_kmh })}
              series={[
                { key: "cong", label: "Congestion", color: "#f28c38", format: (v) => fmt.pct(v) },
                { key: "speed", label: "Speed", color: "#4c8dff", type: "line", axis: "right", format: (v) => `${v.toFixed(0)}` },
              ]}
            />
          </Section>
          <Section title="Segment">
            <div>
              <Attr k="Length" v={`${fmt.int(r.length_m)} m`} />
              <Attr k="Lanes per direction" v={r.lanes} />
              <Attr k="Speed limit" v={`${r.speed_kmh} km/h`} />
              <Attr k="District" v={`${city.districts[r.district].id} · ${city.districts[r.district].name}`} />
            </div>
          </Section>
        </>
      );
    }
  } else if (kind === "node") {
    const n = city.nodes.find((x) => x.id === id);
    const o = telemetry?.nodes.find((x) => x.id === id);
    if (n) {
      title = n.name;
      subtitle = `Intersection ${n.id} · ${n.signalized ? "signalised" : "uncontrolled"}`;
      body = (
        <>
          <div className="grid grid-cols-2 gap-2">
            <Stat label="Queue (avg)" value={fmt.num(o?.queue_veh ?? 0, 0)} unit="veh" />
            <Stat label="Cycle" value={String(n.cycle_s ?? "—")} unit="s" />
          </div>
          <Section title="Queue, last 3 simulated hours">
            <History<NodeObs>
              kind="node"
              id={id}
              marker={marker}
              map={(v) => ({ q: v.queue_veh })}
              series={[{ key: "q", label: "Queue", color: "#f2c037", format: (v) => v.toFixed(0) }]}
            />
          </Section>
          {n.signalized && (
            <Section title="Signal plan">
              <div>
                <Attr k="North–south green" v={`${n.green_ns_s} s`} />
                <Attr k="East–west green" v={`${(n.cycle_s ?? 0) - (n.green_ns_s ?? 0)} s`} />
                <Attr k="Offset" v={`${n.offset_s} s`} />
                <Attr k="Major junction" v={n.major ? "yes" : "no"} />
              </div>
            </Section>
          )}
        </>
      );
    }
  } else if (kind === "district" || kind === "substation" || kind === "school") {
    const fac = kind === "district" ? null : city.facilities.find((f) => f.id === id);
    const d = fac ? city.districts[fac.district] : city.districts.find((x) => x.id === id);
    const o = d ? telemetry?.districts[d.index] : undefined;
    if (d) {
      title = fac ? fac.name : `${d.id} · ${d.name}`;
      subtitle = fac ? `${kind} in ${d.id} · ${d.name}` : `${d.type} district`;
      const showEnergy = kind !== "school";
      body = (
        <>
          {o && (
            <div className="grid grid-cols-2 gap-2">
              {showEnergy && <Stat label="Demand" value={fmt.num(o.demand_mw)} unit={`/ ${o.capacity_mw} MW`} />}
              {showEnergy && <Stat label="Grid load" value={fmt.pct(o.utilization)} color={utilColor(o.utilization)} />}
              <Stat label="PM2.5 (field avg)" value={fmt.num(o.pm25)} unit="µg/m³" color={pmColor(o.pm25)} />
              <Stat label="Vehicles" value={fmt.int(o.vehicles)} />
              {!showEnergy && <Stat label="Students" value={fmt.int(fac?.capacity ?? 0)} />}
              {kind === "district" && <Stat label="EMS calls (1h)" value={String(o.calls_1h)} unit={o.calls_1h ? `ETA ${o.avg_eta_min}m` : ""} />}
            </div>
          )}
          <Section title={showEnergy ? "Electricity demand, last 3 simulated hours" : "District air quality, last 3 simulated hours"}>
            {showEnergy ? (
              <History<DistrictObs>
                kind="district"
                id={d.id}
                marker={marker}
                map={(v) => ({ demand: v.demand_mw, cap: v.capacity_mw })}
                series={[
                  { key: "demand", label: "Demand", color: "#4c8dff", format: (v) => `${v.toFixed(0)}` },
                  { key: "cap", label: "Capacity", color: "#f0565b", type: "line", dashed: true, format: (v) => `${v.toFixed(0)}` },
                ]}
              />
            ) : (
              <History<DistrictObs>
                kind="district"
                id={d.id}
                marker={marker}
                map={(v) => ({ pm: v.pm25 })}
                series={[{ key: "pm", label: "PM2.5", color: "#f28c38", format: (v) => v.toFixed(0) }]}
              />
            )}
          </Section>
          <Section title="District profile">
            <div>
              <Attr k="Population" v={fmt.int(d.population)} />
              <Attr k="Jobs" v={fmt.int(d.jobs)} />
              <Attr k="Floor-area ratio" v={d.building_density.toFixed(2)} />
              <Attr k="Base load" v={`${d.base_load_mw} MW`} />
              <Attr k="Substation capacity" v={`${d.substation_mw} MW`} />
            </div>
          </Section>
        </>
      );
    }
  } else if (kind === "station") {
    const f = city.facilities.find((x) => x.id === id);
    const o = telemetry?.stations.find((x) => x.id === id);
    if (f) {
      title = f.name;
      subtitle = `Air-quality station ${f.id}`;
      body = (
        <>
          <div className="grid grid-cols-2 gap-2">
            <Stat label="PM2.5" value={o?.pm25 != null ? fmt.num(o.pm25) : "—"} unit="µg/m³" color={pmColor(o?.pm25)} />
            <Stat label="Sensor" value={o?.status ?? "—"} color={o?.status === "ok" ? "#3ecf8e" : "#f5a524"} />
          </div>
          <p className="text-xs text-muted-foreground">
            {pmLabel(o?.pm25)} on the US EPA colour scale. Synthetic value from a dispersion model, not an environmental measurement.
            {o?.status === "stuck" && " The sensor is stuck and is repeating its last reading."}
          </p>
          <Section title="Readings, last 3 simulated hours">
            <History<StationObs>
              kind="station"
              id={id}
              marker={marker}
              map={(v) => ({ pm: v.status === "offline" ? null : v.pm25 })}
              series={[{ key: "pm", label: "PM2.5", color: "#f28c38", format: (v) => v.toFixed(0) }]}
            />
          </Section>
        </>
      );
    }
  } else if (kind === "hospital") {
    const f = city.facilities.find((x) => x.id === id);
    const o = telemetry?.hospitals.find((x) => x.id === id);
    if (f) {
      title = f.name;
      subtitle = `Hospital · ${f.capacity} beds`;
      body = (
        <>
          <div className="grid grid-cols-2 gap-2">
            <Stat label="Calls (1h)" value={String(o?.calls_1h ?? 0)} />
            <Stat label="Avg response" value={o?.calls_1h ? fmt.num(o.avg_eta_min) : "—"} unit="min" />
          </div>
          <Section title="Emergency load, last 3 simulated hours">
            <History<HospitalObs>
              kind="hospital"
              id={id}
              marker={marker}
              map={(v) => ({ calls: v.calls_1h, eta: v.calls_1h ? v.avg_eta_min : null })}
              series={[
                { key: "calls", label: "Calls (rolling 1h)", color: "#f07a95", format: (v) => v.toFixed(0) },
                { key: "eta", label: "Avg ETA (min)", color: "#4c8dff", type: "line", axis: "right", format: (v) => v.toFixed(1) },
              ]}
            />
          </Section>
        </>
      );
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-start gap-3 border-b border-border px-4 py-3">
        <div className="min-w-0 flex-1">
          <div className="eyebrow">{kind}</div>
          <h2 className="truncate text-[15px] font-semibold">{title}</h2>
          <p className="truncate text-xs capitalize text-muted-foreground">{subtitle}</p>
        </div>
        <button type="button" onClick={onClose} className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground" aria-label="Close inspector">
          <X className="size-4" />
        </button>
      </div>
      <div className="flex-1 space-y-4 overflow-y-auto px-4 py-4">
        {body ?? <p className="text-sm text-muted-foreground">This entity is not part of the current run.</p>}
        <RelatedAlerts alerts={alerts} />
        {(kind === "road" || kind === "station") && <ForecastPanel city={city} kind={kind} id={id} />}
        {(kind === "district" || kind === "substation") && (
          <ForecastPanel
            city={city}
            kind="district"
            id={kind === "district" ? id : city.districts[city.facilities.find((f) => f.id === id)?.district ?? 0].id}
          />
        )}
      </div>
    </div>
  );
}
