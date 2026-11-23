"use client";

import { useEffect, useState } from "react";
import { api } from "./api";
import type { AnomaliesPayload, Envelope, ForecastPayload, ModelStatus } from "./types";

/** horizon -> forecast value for one entity, or null if the model doesn't cover it. */
export function forecastFor(env: Envelope<ForecastPayload> | undefined, entityId: string): Map<number, number> | null {
  if (!env) return null;
  const i = env.payload.entities.indexOf(entityId);
  if (i < 0) return null;
  const out = new Map<number, number>();
  for (const h of env.payload.horizons) out.set(h, env.payload.values[String(h)][i]);
  return out;
}

export function horizonLabel(h: number, unit: "min" | "h" | string) {
  return unit === "h" ? `+${h} h` : `+${h} min`;
}

export function anomalyFor(env: Envelope<AnomaliesPayload> | null, entityId: string) {
  return env?.payload.flagged.find((a) => a.entity_id === entityId) ?? null;
}

/** Model service status and live accuracy from the gateway, refreshed every 10 s. */
export function useModelStatus() {
  const [status, setStatus] = useState<ModelStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () =>
      api.models().then(
        (s) => alive && (setStatus(s), setError(null)),
        (e: Error) => alive && setError(e.message),
      );
    load();
    const t = setInterval(load, 10_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);
  return { status, error };
}

export const MODEL_LABEL: Record<string, string> = {
  traffic: "Traffic forecast",
  energy: "Energy forecast",
  pm25: "PM2.5 forecast",
  anomaly: "Anomaly detector",
};
