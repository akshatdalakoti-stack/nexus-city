import { create } from "zustand";
import type { AnomaliesPayload, City, Clock, Envelope, ForecastPayload, Frame, ScenarioUpdate, SimEvent, Telemetry } from "./types";

export type ConnState = "connecting" | "open" | "closed";

interface LiveState {
  conn: ConnState;
  runId: string | null;
  city: City | null;
  cityError: string | null;
  clock: Clock | null;
  /** Latest simulated time seen on any message. */
  simTime: string | null;
  telemetry: Telemetry | null;
  /** Increments on every telemetry message; hooks use it to refresh history. */
  telemetrySeq: number;
  events: Envelope<SimEvent>[];
  /** Latest forecast per model (traffic, energy, pm25). */
  forecasts: Record<string, Envelope<ForecastPayload>>;
  anomalies: Envelope<AnomaliesPayload> | null;
  /** Latest status of each scenario seen on the WebSocket. */
  scenarioUpdates: Record<string, ScenarioUpdate>;
  setConn: (c: ConnState) => void;
  setCity: (city: City | null, error?: string | null) => void;
  onRun: (runId: string) => void;
  onClock: (env: Envelope<Clock>) => void;
  onTelemetry: (env: Envelope<Telemetry>) => void;
  onEvent: (env: Envelope<SimEvent>) => void;
  seedEvents: (envs: Envelope<SimEvent>[]) => void;
  onFrame: (env: Envelope<Frame>) => void;
  onForecast: (env: Envelope<ForecastPayload>) => void;
  onAnomalies: (env: Envelope<AnomaliesPayload>) => void;
  onScenario: (env: Envelope<ScenarioUpdate>) => void;
}

const MAX_EVENTS = 120;

export const useLive = create<LiveState>((set, get) => ({
  conn: "connecting",
  runId: null,
  city: null,
  cityError: null,
  clock: null,
  simTime: null,
  telemetry: null,
  telemetrySeq: 0,
  events: [],
  forecasts: {},
  anomalies: null,
  scenarioUpdates: {},
  setConn: (conn) => set({ conn }),
  setCity: (city, error = null) => set({ city, cityError: error }),
  onRun: (runId) => {
    if (get().runId === runId) return;
    set({ runId, telemetry: null, events: [], city: null, forecasts: {}, anomalies: null });
    vehicleBuffer.reset();
  },
  onClock: (env) =>
    set((s) => ({
      ...(s.runId === env.run_id ? {} : { runId: env.run_id }),
      clock: env.payload,
      ...(s.simTime === env.sim_time ? {} : { simTime: env.sim_time }),
    })),
  onTelemetry: (env) =>
    set((s) => ({ telemetry: env.payload, telemetrySeq: s.telemetrySeq + 1, simTime: env.sim_time })),
  onEvent: (env) => set((s) => ({ events: [env, ...s.events].slice(0, MAX_EVENTS) })),
  seedEvents: (envs) => set({ events: envs.slice(0, MAX_EVENTS) }),
  onForecast: (env) =>
    set((s) => (env.run_id === s.runId ? { forecasts: { ...s.forecasts, [env.payload.model]: env } } : {})),
  onAnomalies: (env) => set((s) => (env.run_id === s.runId ? { anomalies: env } : {})),
  onScenario: (env) => set((s) => ({ scenarioUpdates: { ...s.scenarioUpdates, [env.payload.id]: env.payload } })),
  onFrame: (env) => {
    vehicleBuffer.push(env.payload);
    set((s) => (s.simTime === env.sim_time ? {} : { simTime: env.sim_time }));
  },
}));

/**
 * Vehicle frames arrive ~5 times per second. They are kept outside React
 * state so the map can interpolate positions every animation frame without
 * re-rendering components.
 */
class VehicleBuffer {
  prev = new Map<number, [number, number]>();
  next = new Map<number, { lon: number; lat: number; heading: number; speed: number }>();
  startedAt = 0;
  duration = 200;
  private lastArrival = 0;
  version = 0;

  reset() {
    this.prev.clear();
    this.next.clear();
    this.version++;
  }

  /** Positions interpolated for wall time `now` (ms). */
  positionsAt(now: number) {
    const a = Math.min(1, Math.max(0, (now - this.startedAt) / this.duration));
    const out: { id: number; lon: number; lat: number; heading: number; speed: number }[] = [];
    for (const [id, n] of this.next) {
      const p = this.prev.get(id);
      out.push({
        id,
        lon: p ? p[0] + (n.lon - p[0]) * a : n.lon,
        lat: p ? p[1] + (n.lat - p[1]) * a : n.lat,
        heading: n.heading,
        speed: n.speed,
      });
    }
    return out;
  }

  push(f: Frame) {
    const now = performance.now();
    // Start the new segment from where vehicles are drawn right now so motion stays continuous.
    const current = new Map<number, [number, number]>();
    for (const v of this.positionsAt(now)) current.set(v.id, [v.lon, v.lat]);
    this.prev = current;
    this.next = new Map(f.vehicles.map(([id, lon, lat, heading, speed]) => [id, { lon, lat, heading, speed }]));
    if (this.lastArrival > 0) {
      const gap = now - this.lastArrival;
      this.duration = Math.min(1000, Math.max(80, this.duration * 0.7 + gap * 0.3));
    }
    this.lastArrival = now;
    this.startedAt = now;
    this.version++;
  }
}

export const vehicleBuffer = new VehicleBuffer();
