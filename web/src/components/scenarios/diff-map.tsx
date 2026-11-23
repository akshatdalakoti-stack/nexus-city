"use client";

import { useEffect, useRef } from "react";
import {
  Map as MLMap,
  Popup,
  prewarm,
  setWorkerUrl,
  type ExpressionSpecification,
  type GeoJSONSource,
  type StyleSpecification,
} from "maplibre-gl";
import type { FeatureCollection } from "geojson";
import type { City, ScenarioResult } from "@/lib/types";

setWorkerUrl("/vendor/maplibre-gl-worker.mjs");
prewarm();

const STYLE: StyleSpecification = {
  version: 8,
  sources: {},
  layers: [{ id: "bg", type: "background", paint: { "background-color": "#0b0b0d" } }],
};

/** Diverging scale for congestion change: relief is blue, worsening red. */
const deltaColor: ExpressionSpecification = [
  "interpolate",
  ["linear"],
  ["get", "delta"],
  -0.2, "#4c8dff",
  -0.05, "#7fb0ff",
  0, "#3f3f46",
  0.05, "#f28c38",
  0.2, "#f0565b",
];

interface Props {
  city: City;
  result: ScenarioResult;
  districtMetric: "pm25" | "demand" | "none";
}

function roadsFC(city: City, r: ScenarioResult): FeatureCollection {
  const by = new Map(r.roads.map((x) => [x.id, x]));
  return {
    type: "FeatureCollection",
    features: city.roads.map((road) => {
      const d = by.get(road.id);
      return {
        type: "Feature",
        geometry: { type: "LineString", coordinates: road.coords },
        properties: { id: road.id, name: road.name, segment: road.segment, class: road.class, delta: d?.delta ?? 0, base: d?.baseline ?? 0, scen: d?.scenario ?? 0, closed: !!d?.closed },
      };
    }),
  };
}

function districtsFC(city: City, r: ScenarioResult, metric: Props["districtMetric"]): FeatureCollection {
  const by = new Map(r.districts.map((x) => [x.id, x]));
  return {
    type: "FeatureCollection",
    features: city.districts.map((d) => {
      const x = by.get(d.id);
      const rel =
        metric === "pm25" && x ? (x.pm25_scenario - x.pm25_baseline) / Math.max(x.pm25_baseline, 1)
        : metric === "demand" && x ? (x.demand_scenario_mw - x.demand_baseline_mw) / Math.max(x.demand_baseline_mw, 1)
        : 0;
      return { type: "Feature", geometry: { type: "Polygon", coordinates: [d.polygon] }, properties: { id: d.id, name: d.name, rel } };
    }),
  };
}

export function DiffMap({ city, result, districtMetric }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const mapRef = useRef<MLMap | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    const map = new MLMap({
      container: ref.current,
      style: STYLE,
      bounds: city.bounds,
      fitBoundsOptions: { padding: 24 },
      attributionControl: false,
      dragRotate: false,
    });
    mapRef.current = map;
    map.on("load", () => {
      map.addSource("districts", { type: "geojson", data: districtsFC(city, result, districtMetric) });
      map.addSource("roads", { type: "geojson", data: roadsFC(city, result) });
      map.addLayer({
        id: "district-fill",
        type: "fill",
        source: "districts",
        paint: {
          "fill-color": ["interpolate", ["linear"], ["get", "rel"], -0.2, "#3366cc", 0, "#17181c", 0.2, "#c2410c", 0.6, "#dc3b40"],
          "fill-opacity": districtMetric === "none" ? 0 : 0.45,
        },
      });
      map.addLayer({ id: "district-line", type: "line", source: "districts", paint: { "line-color": "#2c2c31", "line-width": 1 } });
      map.addLayer({
        id: "roads",
        type: "line",
        source: "roads",
        layout: { "line-cap": "round" },
        paint: {
          "line-color": deltaColor,
          "line-width": ["match", ["get", "class"], "highway", 4.5, "arterial", 3, 1.8],
        },
      });
      map.addLayer({
        id: "closed",
        type: "line",
        source: "roads",
        filter: ["==", ["get", "closed"], true],
        paint: { "line-color": "#ffffff", "line-width": 6, "line-dasharray": [1, 1] },
      });
      const popup = new Popup({ closeButton: false, closeOnClick: false, offset: 10, className: "nx-popup" });
      map.on("mousemove", "roads", (e) => {
        const p = e.features?.[0]?.properties;
        if (!p) return;
        map.getCanvas().style.cursor = "pointer";
        const pct = (v: number) => `${(v * 100).toFixed(0)}%`;
        popup
          .setLngLat(e.lngLat)
          .setHTML(`<b>${p.name}</b><span>${p.segment}</span><span>${p.closed ? "CLOSED · " : ""}congestion ${pct(p.base)} → ${pct(p.scen)} (${p.delta >= 0 ? "+" : ""}${(p.delta * 100).toFixed(1)} pts)</span>`)
          .addTo(map);
      });
      map.on("mouseleave", "roads", () => {
        map.getCanvas().style.cursor = "";
        popup.remove();
      });
    });
    return () => {
      map.remove();
      mapRef.current = null;
    };
    // The map is rebuilt per city; data updates go through setData below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [city]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !map.isStyleLoaded()) return;
    (map.getSource("roads") as GeoJSONSource | undefined)?.setData(roadsFC(city, result));
    (map.getSource("districts") as GeoJSONSource | undefined)?.setData(districtsFC(city, result, districtMetric));
    if (map.getLayer("district-fill")) map.setPaintProperty("district-fill", "fill-opacity", districtMetric === "none" ? 0 : 0.45);
  }, [city, result, districtMetric]);

  return (
    <div className="relative h-full w-full">
      <div ref={ref} className="h-full w-full" aria-label="Map of the scenario's effect on congestion" role="img" />
      <div className="pointer-events-none absolute bottom-2 left-2 rounded-md bg-raised px-2 py-1.5 text-[12px] text-muted-foreground">
        <div className="mb-1 h-1.5 w-40 rounded-full" style={{ background: "linear-gradient(90deg,#4c8dff,#7fb0ff,#3f3f46,#f28c38,#f0565b)" }} />
        <div className="flex w-40 justify-between font-mono">
          <span>relieved</span>
          <span>worse</span>
        </div>
      </div>
    </div>
  );
}
