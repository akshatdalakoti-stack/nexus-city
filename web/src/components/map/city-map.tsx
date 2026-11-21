"use client";

import { useEffect, useRef, useState } from "react";
import {
  Map as MLMap,
  Marker,
  NavigationControl,
  Popup,
  prewarm,
  setWorkerUrl,
  type ExpressionSpecification,
  type GeoJSONSource,
  type MapGeoJSONFeature,
  type StyleSpecification,
} from "maplibre-gl";
import { congestionStops, fmt, pmLabel } from "@/lib/format";
import { vehicleBuffer } from "@/lib/store";
import type { AnomalyScore, City, Selection, SelectionKind, Telemetry } from "@/lib/types";
import { alertsFC, districtsFC, facilitiesFC, modelAnomaliesFC, nodesFC, pmFC, roadsFC, vehiclesFC } from "./layers";

setWorkerUrl("/vendor/maplibre-gl-worker.mjs");
// Keep MapLibre's worker pool alive across map instances. Without this the
// pool is torn down when the last map is removed, and a map created right
// after (React remounts, a new simulation run) can stall before loading.
prewarm();

export interface LayerToggles {
  traffic: boolean;
  vehicles: boolean;
  air: boolean;
  energy: boolean;
  infrastructure: boolean;
  alerts: boolean;
  /** Colour roads by the traffic model's 30-minute forecast instead of now. */
  forecast: boolean;
  /** Entities the anomaly detector currently flags. */
  anomalies: boolean;
}

interface Props {
  city: City;
  telemetry: Telemetry | null;
  layers: LayerToggles;
  selection: Selection | null;
  onSelect: (s: Selection | null) => void;
  /** Vehicles are only drawn live; replay snapshots do not record them. */
  live: boolean;
  roadForecast?: Map<string, number> | null;
  modelAnomalies?: AnomalyScore[] | null;
}

const STYLE: StyleSpecification = {
  version: 8,
  sources: {},
  layers: [{ id: "bg", type: "background", paint: { "background-color": "#0b0b0d" } }],
};

const congestionColor: ExpressionSpecification = [
  "interpolate",
  ["linear"],
  ["get", "cong"],
  ...congestionStops.flat(),
] as unknown as ExpressionSpecification;

const roadWidth = (scale: number): ExpressionSpecification => [
  "interpolate",
  ["exponential", 1.6],
  ["zoom"],
  11,
  ["*", scale, ["match", ["get", "class"], "highway", 2.2, "arterial", 1.4, 0.7]],
  15,
  ["*", scale, ["match", ["get", "class"], "highway", 9, "arterial", 6, 3]],
];

const HIT_LAYERS = ["facility-circle", "node-circle", "road-hit", "district-fill"];

const LAYER_GROUPS: Record<keyof LayerToggles, string[]> = {
  traffic: ["road-line", "road-incident", "node-circle"],
  vehicles: ["vehicle-dot"],
  air: ["pm-heat"],
  energy: ["district-energy"],
  infrastructure: ["facility-circle"],
  alerts: ["alert-pulse", "alert-dot"],
  forecast: [],
  anomalies: ["model-anomaly"],
};

function kindOf(f: MapGeoJSONFeature): SelectionKind | null {
  switch (f.layer.id) {
    case "road-hit":
      return "road";
    case "node-circle":
      return "node";
    case "district-fill":
      return "district";
    case "facility-circle":
      return ({ aq_station: "station", hospital: "hospital", school: "school", substation: "substation" } as const)[
        f.properties.kind as "aq_station" | "hospital" | "school" | "substation"
      ];
  }
  return null;
}

function hoverText(f: MapGeoJSONFeature): string {
  const p = f.properties;
  switch (f.layer.id) {
    case "road-hit":
      return `<b>${p.name}</b><span>${p.segment}</span><span>${fmt.num(p.speed, 0)} km/h · congestion ${fmt.pct(p.cong)}</span>`;
    case "node-circle":
      return `<b>${p.name}</b><span>Signal · queue ${fmt.num(p.queue, 0)} veh</span>`;
    case "district-fill":
      return `<b>${p.id} · ${p.name}</b><span>${p.type} · grid ${fmt.pct(p.util)}</span>`;
    case "facility-circle":
      if (p.kind === "aq_station")
        return `<b>${p.name}</b><span>${p.status !== "offline" && p.pm >= 0 ? `PM2.5 ${fmt.num(p.pm)} µg/m³ · ${pmLabel(p.pm)}` : "Sensor offline"}${p.status === "stuck" ? " (stuck)" : ""}</span>`;
      if (p.kind === "substation") return `<b>${p.name}</b><span>Load ${fmt.pct(p.util)}</span>`;
      return `<b>${p.name}</b><span>${String(p.kind).replace("_", " ")}</span>`;
  }
  return "";
}

export function CityMap({ city, telemetry, layers, selection, onSelect, live, roadForecast, modelAnomalies }: Props) {
  const container = useRef<HTMLDivElement>(null);
  const mapRef = useRef<MLMap | null>(null);
  const [ready, setReady] = useState(false);
  const onSelectRef = useRef(onSelect);
  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

  // Create the map once per city.
  useEffect(() => {
    if (!container.current) return;
    const [w, s, e, n] = city.bounds;
    const padLon = (e - w) * 1.2;
    const padLat = (n - s) * 1.2;
    const map = new MLMap({
      container: container.current,
      style: STYLE,
      bounds: [w, s, e, n],
      // Keep the city clear of the floating layer panel, inspector and replay bar.
      fitBoundsOptions: { padding: { top: 40, bottom: 110, left: 270, right: 380 } },
      maxBounds: [w - padLon, s - padLat, e + padLon, n + padLat],
      attributionControl: false,
      maxPitch: 60,
    });
    mapRef.current = map;
    map.addControl(new NavigationControl({ visualizePitch: true }), "bottom-right");

    const markers: Marker[] = [];
    map.on("load", () => {
      const src = (id: string, data: GeoJSON.FeatureCollection) => map.addSource(id, { type: "geojson", data });
      src("districts", districtsFC(city, null));
      src("pm", pmFC(city, null));
      src("roads", roadsFC(city, null));
      src("nodes", nodesFC(city, null));
      src("vehicles", vehiclesFC([]));
      src("facilities", facilitiesFC(city, null));
      src("alerts", alertsFC(city, null));
      src("model-anomalies", modelAnomaliesFC(city, null));

      map.addLayer({
        id: "district-fill",
        type: "fill",
        source: "districts",
        paint: {
          "fill-color": ["match", ["get", "type"], "residential", "#16171b", "commercial", "#17181d", "industrial", "#1b1915", "#18171c"],
          "fill-opacity": 1,
        },
      });
      map.addLayer({
        id: "district-energy",
        type: "fill",
        source: "districts",
        layout: { visibility: "none" },
        paint: {
          "fill-color": ["interpolate", ["linear"], ["get", "util"], 0.3, "#1e2a3d", 0.6, "#2fa36b", 0.75, "#e3c13b", 0.9, "#f28c38", 1, "#f0565b"],
          "fill-opacity": 0.35,
        },
      });
      map.addLayer({
        id: "pm-heat",
        type: "heatmap",
        source: "pm",
        paint: {
          "heatmap-weight": ["interpolate", ["linear"], ["get", "pm"], 10, 0, 25, 0.25, 40, 0.55, 60, 0.85, 100, 1],
          "heatmap-intensity": ["interpolate", ["linear"], ["zoom"], 11, 0.5, 13, 0.32, 15, 0.4],
          "heatmap-radius": ["interpolate", ["exponential", 2], ["zoom"], 11, 9, 13, 30, 15, 120],
          "heatmap-color": [
            "interpolate",
            ["linear"],
            ["heatmap-density"],
            0,
            "rgba(0,0,0,0)",
            0.2,
            "rgba(47,163,107,0.16)",
            0.45,
            "rgba(227,193,59,0.28)",
            0.7,
            "rgba(242,140,56,0.4)",
            0.9,
            "rgba(240,86,91,0.5)",
            1,
            "rgba(163,36,43,0.6)",
          ],
        },
      });
      map.addLayer({
        id: "district-line",
        type: "line",
        source: "districts",
        paint: { "line-color": "#2c2c31", "line-width": 1 },
      });
      map.addLayer({
        id: "road-casing",
        type: "line",
        source: "roads",
        layout: { "line-cap": "round", "line-join": "round" },
        paint: { "line-color": "#0b0b0d", "line-width": roadWidth(1.8) },
      });
      map.addLayer({
        id: "road-line",
        type: "line",
        source: "roads",
        layout: { "line-cap": "round", "line-join": "round" },
        paint: { "line-color": congestionColor, "line-width": roadWidth(1), "line-opacity": 0.95 },
      });
      map.addLayer({
        id: "road-incident",
        type: "line",
        source: "roads",
        filter: ["==", ["get", "incident"], true],
        paint: { "line-color": "#f0565b", "line-width": roadWidth(2.2), "line-dasharray": [1, 1.2], "line-opacity": 0.9 },
      });
      map.addLayer({
        id: "road-hit",
        type: "line",
        source: "roads",
        paint: { "line-color": "#000", "line-width": 14, "line-opacity": 0 },
      });
      map.addLayer({
        id: "node-circle",
        type: "circle",
        source: "nodes",
        paint: {
          "circle-radius": ["interpolate", ["linear"], ["zoom"], 11, ["case", ["get", "major"], 2, 1.2], 15, ["+", 4, ["min", 8, ["/", ["get", "queue"], 15]]]],
          "circle-color": "#0b0b0d",
          "circle-stroke-color": ["interpolate", ["linear"], ["get", "queue"], 0, "#52525b", 40, "#f5a524", 120, "#f0565b"],
          "circle-stroke-width": 1.2,
        },
      });
      map.addLayer({
        id: "vehicle-dot",
        type: "circle",
        source: "vehicles",
        paint: {
          "circle-radius": ["interpolate", ["linear"], ["zoom"], 11, 1, 15, 3.2],
          "circle-color": ["interpolate", ["linear"], ["get", "speed"], 5, "#f5a524", 25, "#d4d4d8", 60, "#fafafa"],
          "circle-opacity": 0.9,
        },
      });
      map.addLayer({
        id: "facility-circle",
        type: "circle",
        source: "facilities",
        paint: {
          "circle-radius": ["interpolate", ["linear"], ["zoom"], 11, ["match", ["get", "kind"], "hospital", 4.5, 3.2], 15, ["match", ["get", "kind"], "hospital", 9, 7]],
          "circle-color": "#0b0b0d",
          "circle-stroke-width": ["match", ["get", "kind"], "hospital", 2.2, 1.6],
          "circle-stroke-color": [
            "match",
            ["get", "kind"],
            "aq_station",
            ["case", ["<", ["get", "pm"], 0], "#71717a", ["interpolate", ["linear"], ["get", "pm"], 0, "#3ecf8e", 12, "#f2c037", 35.5, "#f28c38", 55.5, "#f0565b", 150, "#a3242b"]],
            "substation",
            ["interpolate", ["linear"], ["get", "util"], 0.5, "#3ecf8e", 0.75, "#f2c037", 0.9, "#f28c38", 1, "#f0565b"],
            "hospital",
            "#f07a95",
            "#a78bfa",
          ],
        },
      });
      map.addLayer({
        id: "alert-pulse",
        type: "circle",
        source: "alerts",
        paint: {
          "circle-radius": 10,
          "circle-color": "rgba(0,0,0,0)",
          "circle-stroke-width": 1.5,
          "circle-stroke-color": ["match", ["get", "severity"], "critical", "#f0565b", "warning", "#f5a524", "#7fb0ff"],
          "circle-stroke-opacity": 0.6,
        },
      });
      map.addLayer({
        id: "alert-dot",
        type: "circle",
        source: "alerts",
        paint: {
          "circle-radius": 3,
          "circle-color": ["match", ["get", "severity"], "critical", "#f0565b", "warning", "#f5a524", "#7fb0ff"],
        },
      });
      map.addLayer({
        id: "model-anomaly",
        type: "circle",
        source: "model-anomalies",
        paint: {
          "circle-radius": ["interpolate", ["linear"], ["zoom"], 11, 7, 15, 16],
          "circle-color": "rgba(0,0,0,0)",
          "circle-stroke-color": "#a78bfa",
          "circle-stroke-width": 1.6,
          "circle-stroke-opacity": 0.9,
        },
      });
      // Selection highlights.
      map.addLayer({
        id: "sel-road",
        type: "line",
        source: "roads",
        filter: ["==", ["get", "id"], ""],
        layout: { "line-cap": "round" },
        paint: { "line-color": "#ffffff", "line-width": roadWidth(1.7), "line-opacity": 0.85 },
      });
      map.addLayer({
        id: "sel-district",
        type: "line",
        source: "districts",
        filter: ["==", ["get", "id"], ""],
        paint: { "line-color": "#4c8dff", "line-width": 2 },
      });
      map.addLayer({
        id: "sel-point",
        type: "circle",
        source: "facilities",
        filter: ["==", ["get", "id"], ""],
        paint: { "circle-radius": 14, "circle-color": "rgba(0,0,0,0)", "circle-stroke-color": "#ffffff", "circle-stroke-width": 1.5 },
      });
      map.addLayer({
        id: "sel-node",
        type: "circle",
        source: "nodes",
        filter: ["==", ["get", "id"], ""],
        paint: { "circle-radius": 11, "circle-color": "rgba(0,0,0,0)", "circle-stroke-color": "#ffffff", "circle-stroke-width": 1.5 },
      });

      for (const d of city.districts) {
        const el = document.createElement("div");
        el.className = "district-label";
        el.innerHTML = `<b>${d.id}</b><span>${d.name}</span>`;
        markers.push(new Marker({ element: el }).setLngLat(d.centroid).addTo(map));
      }
      // Names collide when zoomed out; show only the district code then.
      const compact = () => container.current?.classList.toggle("labels-compact", map.getZoom() < 13.8);
      map.on("zoom", compact);
      compact();

      // Interaction.
      const popup = new Popup({ closeButton: false, closeOnClick: false, offset: 12, className: "nx-popup" });
      const pick = (point: { x: number; y: number }) => {
        const feats = map.queryRenderedFeatures([point.x, point.y], { layers: HIT_LAYERS });
        for (const id of HIT_LAYERS) {
          const f = feats.find((x) => x.layer.id === id);
          if (f) return f;
        }
        return null;
      };
      map.on("mousemove", (e) => {
        const f = pick(e.point);
        map.getCanvas().style.cursor = f && f.layer.id !== "district-fill" ? "pointer" : "";
        if (f) popup.setLngLat(e.lngLat).setHTML(hoverText(f)).addTo(map);
        else popup.remove();
      });
      map.on("mouseout", () => popup.remove());
      map.on("click", (e) => {
        const f = pick(e.point);
        const kind = f && kindOf(f);
        onSelectRef.current(kind ? { kind, id: String(f.properties.id) } : null);
      });
      setReady(true);
    });

    return () => {
      markers.forEach((m) => m.remove());
      map.remove();
      mapRef.current = null;
      setReady(false);
    };
  }, [city]);

  // Push telemetry into the sources.
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map) return;
    const set = (id: string, data: GeoJSON.FeatureCollection) => (map.getSource(id) as GeoJSONSource | undefined)?.setData(data);
    set("roads", roadsFC(city, telemetry, layers.forecast && live ? roadForecast : null));
    set("districts", districtsFC(city, telemetry));
    set("nodes", nodesFC(city, telemetry));
    set("facilities", facilitiesFC(city, telemetry));
    set("pm", pmFC(city, telemetry));
    set("alerts", alertsFC(city, telemetry?.alerts));
    set("model-anomalies", modelAnomaliesFC(city, live ? modelAnomalies : null));
  }, [ready, city, telemetry, roadForecast, modelAnomalies, layers.forecast, live]);

  // Layer visibility.
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map) return;
    for (const [group, ids] of Object.entries(LAYER_GROUPS) as [keyof LayerToggles, string[]][]) {
      const visible = layers[group] && (group !== "vehicles" || live);
      for (const id of ids) map.setLayoutProperty(id, "visibility", visible ? "visible" : "none");
    }
  }, [ready, layers, live]);

  // Selection highlight.
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map) return;
    const id = selection?.id ?? "";
    const k = selection?.kind;
    map.setFilter("sel-road", ["==", ["get", "id"], k === "road" ? id : ""]);
    map.setFilter("sel-district", ["==", ["get", "id"], k === "district" ? id : ""]);
    map.setFilter("sel-node", ["==", ["get", "id"], k === "node" ? id : ""]);
    const pointKinds = ["station", "hospital", "school", "substation"];
    map.setFilter("sel-point", ["==", ["get", "id"], k && pointKinds.includes(k) ? id : ""]);
  }, [ready, selection]);

  // Animation loop: interpolated vehicles and pulsing alerts.
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map) return;
    let raf = 0;
    let lastVehicles = 0;
    let pending = false; // at most one vehicle update in MapLibre's worker at a time
    let cancelled = false;
    const tick = (now: number) => {
      raf = requestAnimationFrame(tick);
      if (live && layers.vehicles && !pending && now - lastVehicles > 33) {
        const src = map.getSource("vehicles") as GeoJSONSource | undefined;
        if (src) {
          lastVehicles = now;
          pending = true;
          src.setData(vehiclesFC(vehicleBuffer.positionsAt(now))).finally(() => {
            pending = false;
          });
        }
      }
      if (layers.alerts) {
        const phase = (now % 1600) / 1600;
        map.setPaintProperty("alert-pulse", "circle-radius", 6 + 16 * phase);
        map.setPaintProperty("alert-pulse", "circle-stroke-opacity", 0.75 * (1 - phase));
      }
    };
    // Start animating only once the static layers have rendered, so the
    // vehicle stream cannot delay the first draw of roads and facilities.
    const start = () => {
      if (!cancelled && ((live && layers.vehicles) || layers.alerts)) raf = requestAnimationFrame(tick);
    };
    if (map.loaded() && map.areTilesLoaded()) start();
    else map.once("idle", start);
    return () => {
      cancelled = true;
      cancelAnimationFrame(raf);
      map.off("idle", start);
    };
  }, [ready, live, layers.vehicles, layers.alerts]);

  // MapLibre sets position: relative on its container, so the sizing lives on a wrapper.
  return (
    <div className="absolute inset-0">
      <div ref={container} className="h-full w-full" aria-label="Interactive map of Nexus City" role="application" />
    </div>
  );
}
