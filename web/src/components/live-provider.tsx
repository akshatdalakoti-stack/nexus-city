"use client";

import { useEffect } from "react";
import { api, WS_URL } from "@/lib/api";
import { useLive } from "@/lib/store";
import type { AnomaliesPayload, Clock, Envelope, ForecastPayload, Frame, ScenarioUpdate, SimEvent, Telemetry } from "@/lib/types";

type Message =
  | { type: "hello"; data: { run_id: string; version: string } }
  | { type: "run"; data: { run_id: string } }
  | { type: "clock"; data: Envelope<Clock> }
  | { type: "telemetry"; data: Envelope<Telemetry> }
  | { type: "event"; data: Envelope<SimEvent> }
  | { type: "frame"; data: Envelope<Frame> }
  | { type: "forecast"; data: Envelope<ForecastPayload> }
  | { type: "anomaly"; data: Envelope<AnomaliesPayload> }
  | { type: "scenario"; data: Envelope<ScenarioUpdate> };

/**
 * Keeps one WebSocket to the gateway open for the whole app, reconnecting
 * with backoff, and loads the city whenever the simulation run changes.
 */
export function LiveProvider({ children }: { children: React.ReactNode }) {
  const runId = useLive((s) => s.runId);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let retry = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let closed = false;
    const live = useLive.getState();

    const connect = () => {
      live.setConn("connecting");
      ws = new WebSocket(WS_URL);
      ws.onopen = () => {
        retry = 0;
        useLive.getState().setConn("open");
      };
      ws.onmessage = (ev) => {
        let msg: Message;
        try {
          msg = JSON.parse(ev.data as string) as Message;
        } catch {
          return;
        }
        const s = useLive.getState();
        switch (msg.type) {
          case "hello":
            if (msg.data.run_id) s.onRun(msg.data.run_id);
            break;
          case "run":
            s.onRun(msg.data.run_id);
            break;
          case "clock":
            if (s.runId && msg.data.run_id !== s.runId) s.onRun(msg.data.run_id);
            s.onClock(msg.data);
            break;
          case "telemetry":
            s.onTelemetry(msg.data);
            break;
          case "event":
            s.onEvent(msg.data);
            break;
          case "frame":
            s.onFrame(msg.data);
            break;
          case "forecast":
            s.onForecast(msg.data);
            break;
          case "anomaly":
            s.onAnomalies(msg.data);
            break;
          case "scenario":
            s.onScenario(msg.data);
            break;
        }
      };
      ws.onclose = () => {
        if (closed) return;
        useLive.getState().setConn("closed");
        const delay = Math.min(10_000, 500 * 2 ** retry++);
        timer = setTimeout(connect, delay);
      };
    };
    connect();
    return () => {
      closed = true;
      clearTimeout(timer);
      ws?.close();
    };
  }, []);

  // (Re)load the static city and recent events for each run.
  useEffect(() => {
    if (!runId) return;
    let cancelled = false;
    api
      .city()
      .then((city) => !cancelled && useLive.getState().setCity(city))
      .catch((e: Error) => !cancelled && useLive.getState().setCity(null, e.message));
    api
      .events(100)
      .then((evs) => !cancelled && useLive.getState().seedEvents(evs))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [runId]);

  return <>{children}</>;
}
