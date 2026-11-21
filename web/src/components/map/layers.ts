// Builds the GeoJSON the map draws. Geometry comes from the city JSON and
// every value comes from a telemetry snapshot; nothing is generated here.
import type { FeatureCollection, Point } from "geojson";
import type { Alert, AnomalyScore, City, Telemetry } from "@/lib/types";

type FC = FeatureCollection;

const empty = (): FC => ({ type: "FeatureCollection", features: [] });

/** `forecast` (road id -> predicted congestion) replaces current congestion when given. */
export function roadsFC(city: City, tel: Telemetry | null, forecast?: Map<string, number> | null): FC {
  return {
    type: "FeatureCollection",
    features: city.roads.map((r, i) => {
      const o = tel?.roads[i];
      const predicted = forecast?.get(r.id);
      return {
        type: "Feature",
        id: i,
        geometry: { type: "LineString", coordinates: r.coords },
        properties: {
          id: r.id,
          name: r.name,
          segment: r.segment,
          class: r.class,
          cong: predicted ?? o?.congestion ?? 0,
          speed: o?.speed_kmh ?? r.speed_kmh,
          flow: o?.flow_vph ?? 0,
          incident: !!o?.incident,
        },
      };
    }),
  };
}

export function districtsFC(city: City, tel: Telemetry | null): FC {
  return {
    type: "FeatureCollection",
    features: city.districts.map((d, i) => {
      const o = tel?.districts[i];
      return {
        type: "Feature",
        id: i,
        geometry: { type: "Polygon", coordinates: [d.polygon] },
        properties: { id: d.id, name: d.name, type: d.type, util: o?.utilization ?? 0, demand: o?.demand_mw ?? 0 },
      };
    }),
  };
}

export function nodesFC(city: City, tel: Telemetry | null): FC {
  const q = new Map(tel?.nodes.map((n) => [n.id, n.queue_veh]) ?? []);
  return {
    type: "FeatureCollection",
    features: city.nodes
      .filter((n) => n.signalized)
      .map((n) => ({
        type: "Feature",
        geometry: { type: "Point", coordinates: [n.lon, n.lat] },
        properties: { id: n.id, name: n.name, major: n.major, queue: q.get(n.id) ?? 0 },
      })),
  };
}

export function facilitiesFC(city: City, tel: Telemetry | null): FC {
  const st = new Map(tel?.stations.map((s) => [s.id, s]) ?? []);
  return {
    type: "FeatureCollection",
    features: city.facilities.map((f) => {
      const s = st.get(f.id);
      const util = f.kind === "substation" ? (tel?.districts[f.district]?.utilization ?? 0) : 0;
      return {
        type: "Feature",
        geometry: { type: "Point", coordinates: [f.lon, f.lat] },
        properties: {
          id: f.id,
          kind: f.kind,
          name: f.name,
          pm: s?.pm25 ?? -1,
          status: s?.status ?? "ok",
          util,
        },
      };
    }),
  };
}

export function pmFC(city: City, tel: Telemetry | null): FC {
  if (!tel) return empty();
  return {
    type: "FeatureCollection",
    features: city.grid.centers.map((c, i) => ({
      type: "Feature",
      geometry: { type: "Point", coordinates: c },
      properties: { pm: tel.pm_grid[i] ?? 0 },
    })),
  };
}

/** Where on the map an entity sits. */
export function entityLocation(city: City, type: string, id: string): [number, number] | null {
  switch (type) {
    case "road": {
      const r = city.roads.find((x) => x.id === id);
      if (!r) return null;
      return [(r.coords[0][0] + r.coords[1][0]) / 2, (r.coords[0][1] + r.coords[1][1]) / 2];
    }
    case "station":
    case "hospital": {
      const f = city.facilities.find((x) => x.id === id);
      return f ? [f.lon, f.lat] : null;
    }
    case "district": {
      const d = city.districts.find((x) => x.id === id);
      return d ? d.centroid : null;
    }
    case "node": {
      const n = city.nodes.find((x) => x.id === id);
      return n ? [n.lon, n.lat] : null;
    }
  }
  return null;
}

export function alertsFC(city: City, alerts: Alert[] | null | undefined): FC {
  const features: FC["features"] = [];
  for (const a of alerts ?? []) {
    const loc = entityLocation(city, a.entity_type, a.entity_id);
    if (!loc) continue;
    features.push({
      type: "Feature",
      geometry: { type: "Point", coordinates: loc } as Point,
      properties: { id: a.id, severity: a.severity, title: a.title, entity_type: a.entity_type, entity_id: a.entity_id },
    });
  }
  return { type: "FeatureCollection", features };
}

export function modelAnomaliesFC(city: City, flagged: AnomalyScore[] | null | undefined): FC {
  const features: FC["features"] = [];
  for (const a of flagged ?? []) {
    const loc = entityLocation(city, a.entity_type, a.entity_id);
    if (loc) features.push({ type: "Feature", geometry: { type: "Point", coordinates: loc }, properties: { id: a.entity_id, reason: a.reason } });
  }
  return { type: "FeatureCollection", features };
}

export function vehiclesFC(list: { id: number; lon: number; lat: number; speed: number }[]): FC {
  return {
    type: "FeatureCollection",
    features: list.map((v) => ({
      type: "Feature",
      geometry: { type: "Point", coordinates: [v.lon, v.lat] },
      properties: { speed: v.speed },
    })),
  };
}
