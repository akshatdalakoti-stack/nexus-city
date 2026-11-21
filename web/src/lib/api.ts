import type {
  AnomaliesPayload,
  AssistantStatus,
  ChatResponse,
  ChatTurn,
  AnomalyModelMeta,
  City,
  ForecastModelMeta,
  ForecastPayload,
  ForecastPoint,
  InfraInfo,
  InfraSeriesResponse,
  ModelStatus,
  Scenario,
  ScenarioKind,
  ScenarioParams,
  Clock,
  Envelope,
  HistoryResponse,
  RuntimeInfo,
  SimEvent,
  Telemetry,
  TelemetryRange,
} from "./types";

/** Base URL of the Go gateway. Set NEXT_PUBLIC_GATEWAY_URL to override. */
export const GATEWAY_URL = (process.env.NEXT_PUBLIC_GATEWAY_URL ?? "http://localhost:8080").replace(/\/$/, "");

export const WS_URL = GATEWAY_URL.replace(/^http/, "ws") + "/ws";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(GATEWAY_URL + path, { ...init, cache: "no-store" });
  } catch (e) {
    if ((e as Error).name === "TimeoutError") throw new ApiError(0, "The request timed out");
    throw new ApiError(0, `Cannot reach the gateway at ${GATEWAY_URL}`);
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = ((await res.json()) as { error?: string }).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  return (await res.json()) as T;
}

export const api = {
  city: () => request<City>("/api/city"),
  clock: () => request<Envelope<Clock>>("/api/clock"),
  latest: () => request<Envelope<Telemetry>>("/api/telemetry/latest"),
  telemetryAt: (at: string) => request<Envelope<Telemetry>>(`/api/telemetry?at=${encodeURIComponent(at)}`),
  range: () => request<TelemetryRange>("/api/telemetry/range"),
  history: <T>(kind: string, id: string, minutes = 180) =>
    request<HistoryResponse<T>>(`/api/history/${kind}/${encodeURIComponent(id)}?minutes=${minutes}`),
  runtime: () => request<RuntimeInfo>("/api/runtime"),
  models: () => request<ModelStatus[]>("/api/models"),
  modelMeta: <K extends string>(kind: K) =>
    request<K extends "anomaly" ? AnomalyModelMeta : ForecastModelMeta>(`/api/models/${kind}`),
  forecastsLatest: () => request<Record<string, Envelope<ForecastPayload>>>("/api/forecasts/latest"),
  entityForecasts: (model: string, id: string, minutes = 180) =>
    request<{ points: ForecastPoint[] }>(`/api/forecasts/${model}/${encodeURIComponent(id)}?minutes=${minutes}`),
  anomaliesLatest: () => request<Envelope<AnomaliesPayload>>("/api/anomalies/latest"),
  scenarios: (limit = 50) => request<Scenario[]>(`/api/scenarios?limit=${limit}`),
  scenario: (id: string) => request<Scenario>(`/api/scenarios/${encodeURIComponent(id)}`),
  scenarioQueue: () => request<{ waiting: number; running: number; consumers: number }>("/api/scenarios/queue"),
  createScenario: (body: { name?: string; kind: ScenarioKind; params: ScenarioParams; duration_min: number }) =>
    request<Scenario>("/api/scenarios", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  events: (limit = 50) => request<Envelope<SimEvent>[]>(`/api/events?limit=${limit}`),
  control: (body: { action: "pause" | "resume" | "speed"; speed?: number }) =>
    request<{ queued: boolean }>("/api/clock", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  infra: () => request<InfraInfo>("/api/infra"),
  infraSeries: (minutes = 30) => request<InfraSeriesResponse>(`/api/infra/series?minutes=${minutes}`),
  assistantStatus: () => request<AssistantStatus>("/api/assistant/status"),
  /** An answer can include a two-hour what-if simulation, so allow up to 3 minutes. */
  chat: (message: string, history: ChatTurn[]) =>
    request<ChatResponse>("/api/assistant/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, history }),
      signal: AbortSignal.timeout(180_000),
    }),
  reset: (seed?: number) =>
    request<{ queued: boolean }>("/api/sim/reset", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(seed === undefined ? {} : { seed }),
    }),
};
