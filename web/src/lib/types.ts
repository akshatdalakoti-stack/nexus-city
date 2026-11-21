// TypeScript mirror of core/internal/events/events.go and the city JSON.
// Keep the two in sync; docs/events.md describes the contract.

export interface Envelope<T> {
  schema: string;
  run_id: string;
  seed: number;
  tick: number;
  sim_time: string;
  emitted_at: string;
  payload: T;
}

export interface Clock {
  run_id: string;
  seed: number;
  epoch: string;
  sim_time: string;
  tick: number;
  speed: number;
  paused: boolean;
}

/** [id, lon, lat, heading_deg, speed_kmh] */
export type VehicleTuple = [number, number, number, number, number];

export interface Frame {
  sim_time: string;
  tick: number;
  scale: number;
  vehicles: VehicleTuple[];
}

export interface Weather {
  temp_c: number;
  humidity: number;
  wind_speed_ms: number;
  wind_dir_deg: number;
}

export interface RoadObs {
  id: string;
  flow_vph: number;
  speed_kmh: number;
  congestion: number;
  vehicles: number;
  incident?: string;
}

export interface NodeObs {
  id: string;
  queue_veh: number;
}

export interface DistrictObs {
  id: string;
  demand_mw: number;
  capacity_mw: number;
  utilization: number;
  pm25: number;
  vehicles: number;
  calls_1h: number;
  avg_eta_min: number;
}

export interface StationObs {
  id: string;
  pm25: number | null;
  status: "ok" | "stuck" | "offline";
}

export interface HospitalObs {
  id: string;
  calls_1h: number;
  avg_eta_min: number;
}

export interface IncidentObs {
  id: string;
  road_id: string;
  severity: "minor" | "major";
  started_at: string;
  expected_clear_at: string;
}

export type Severity = "info" | "warning" | "critical";

export interface Alert {
  id: string;
  key: string;
  kind: string;
  severity: Severity;
  entity_type: string;
  entity_id: string;
  title: string;
  detail: string;
  value: number;
  source: "rule" | "model";
  opened_at: string;
  resolved_at?: string;
}

export interface Summary {
  vehicles: number;
  agents: number;
  avg_speed_kmh: number;
  avg_congestion: number;
  demand_mw: number;
  capacity_mw: number;
  avg_pm25: number;
  max_pm25: number;
  trips_1h: number;
  avg_trip_min: number;
  calls_1h: number;
  avg_eta_min: number;
  incidents: number;
  alerts: number;
  sensor_faults: number;
}

export interface Telemetry {
  sim_time: string;
  tick: number;
  weather: Weather;
  summary: Summary;
  roads: RoadObs[];
  nodes: NodeObs[];
  districts: DistrictObs[];
  stations: StationObs[];
  hospitals: HospitalObs[];
  incidents: IncidentObs[] | null;
  alerts: Alert[] | null;
  pm_grid: number[];
}

export interface SimEvent {
  kind: string;
  sim_time: string;
  severity: Severity;
  entity_type: string;
  entity_id: string;
  message: string;
  data?: Record<string, unknown>;
}

// ---- City ----

export interface CityNode {
  id: string;
  index: number;
  name: string;
  row: number;
  col: number;
  x: number;
  y: number;
  lon: number;
  lat: number;
  district: number;
  signalized: boolean;
  major: boolean;
  cycle_s?: number;
  green_ns_s?: number;
  offset_s?: number;
}

export interface CityRoad {
  id: string;
  index: number;
  name: string;
  segment: string;
  class: "highway" | "arterial" | "local";
  a: number;
  b: number;
  axis: "ew" | "ns";
  length_m: number;
  lanes: number;
  speed_kmh: number;
  district: number;
  coords: [number, number][];
}

export type DistrictType = "residential" | "commercial" | "industrial" | "mixed";

export interface CityDistrict {
  id: string;
  index: number;
  name: string;
  type: DistrictType;
  polygon: [number, number][];
  centroid: [number, number];
  population: number;
  jobs: number;
  floor_area_m2: number;
  building_density: number;
  base_load_mw: number;
  substation_mw: number;
  nodes: number[];
}

export type FacilityKind = "hospital" | "school" | "aq_station" | "substation";

export interface Facility {
  id: string;
  kind: FacilityKind;
  name: string;
  lon: number;
  lat: number;
  district: number;
  node: number;
  capacity?: number;
}

export interface City {
  name: string;
  version: string;
  seed: number;
  width_m: number;
  height_m: number;
  bounds: [number, number, number, number];
  nodes: CityNode[];
  roads: CityRoad[];
  districts: CityDistrict[];
  facilities: Facility[];
  grid: { cell_m: number; cols: number; rows: number; centers: [number, number][] };
}

// ---- Gateway responses ----

export interface HistoryPoint<T> {
  t: string;
  v: T;
}

export interface HistoryResponse<T> {
  kind: string;
  id: string;
  minutes: number;
  points: HistoryPoint<T>[];
}

export interface TelemetryRange {
  run_id: string;
  from: string | null;
  to: string | null;
  count: number;
}

export interface RuntimeInfo {
  mode: "local" | "kubernetes";
  checked_at: string;
  gateway: {
    version: string;
    hostname: string;
    uptime_s: number;
    ws_clients: number;
    ws_messages_sent: number;
    ws_messages_dropped: number;
    goroutines: number;
    heap_mb: number;
    telemetry_snapshots: number;
  };
  redis: {
    ok: boolean;
    latency_ms: number;
    used_memory_mb: number;
    streams: Record<string, number> | null;
    error?: string;
  };
  simulation: {
    alive: boolean;
    heartbeat_age_s: number | null;
    heartbeat: Record<string, string> | null;
  };
  database?: { configured: boolean; ok: boolean; latency_ms?: number; error?: string };
  scenario_queue?: { waiting: number; running: number; consumers: number };
}

export type SelectionKind = "road" | "node" | "district" | "station" | "hospital" | "school" | "substation";

export interface Selection {
  kind: SelectionKind;
  id: string;
}

// ---- ML (Phase 2) ----

export type ForecastModelKind = "traffic" | "energy" | "pm25";

export interface ForecastPayload {
  model: ForecastModelKind;
  version: string;
  entity_type: "road" | "district" | "station";
  horizon_unit: "min" | "h";
  horizons: number[];
  target_ticks: number[];
  output_unit: string;
  issued_tick: number;
  entities: string[];
  values: Record<string, number[]>;
  latency_ms: number;
}

export interface AnomalyScore {
  entity_type: "road" | "district" | "station";
  entity_id: string;
  score: number;
  anomalous: boolean;
  reason: string;
}

export interface AnomaliesPayload {
  model: "anomaly";
  version: string;
  issued_tick: number;
  flagged: AnomalyScore[];
  top: Record<"traffic" | "energy" | "air", AnomalyScore[]>;
  latency_ms: number;
}

export interface AccuracySummary {
  horizon: number;
  model_mae: number;
  baseline_mae: number | null;
  points: number;
  from_tick: number;
  to_tick: number;
}

export interface ModelServiceInfo {
  kind: string;
  version: string | null;
  description?: string;
  horizons?: number[];
  horizon_unit?: string;
  output_unit?: string;
  host?: string;
  updated_at?: string;
  latency_ms?: number | null;
  error?: string;
}

export interface ModelStatus {
  kind: ForecastModelKind | "anomaly";
  service: ModelServiceInfo | null;
  alive: boolean;
  live_accuracy: AccuracySummary[];
}

export interface RegressionScore {
  mae: number;
  rmse: number;
  n: number;
  mape?: number;
}

/** Per-horizon evaluation entry in forecast model metadata. */
export interface HorizonMetrics {
  model: RegressionScore;
  best_baseline: string;
  mae_improvement_pct: number;
  peak?: Record<string, number>;
  changing?: Record<string, number>;
  [baseline: string]: unknown;
}

export interface ForecastModelMeta {
  kind: ForecastModelKind;
  version: string;
  description: string;
  entity: string;
  horizons: number[];
  horizon_unit: "min" | "h";
  output_unit: string;
  features: string[];
  feature_importance: Record<string, { feature: string; share: number }[]>;
  rows: Record<string, Record<string, number>>;
  baselines: string[];
  metrics: { test: Record<string, HorizonMetrics>; holdout: Record<string, HorizonMetrics> };
  data: { train_seeds: number[]; holdout_seeds: number[]; split_days: Record<string, [number, number]> };
  training_seconds: number;
  created_at: string;
  notes: string;
  library_versions: Record<string, string>;
}

export interface DetectionScore {
  precision: number | null;
  recall: number | null;
  f1: number;
  flag_rate: number;
}

export interface AnomalyModelMeta {
  kind: "anomaly";
  version: string;
  description: string;
  domains: Record<string, { entity: string; features: string[]; threshold: number }>;
  metrics: Record<
    string,
    {
      val: { model: DetectionScore; rule: DetectionScore };
      test: { model: DetectionScore; rule: DetectionScore; events: Record<"model" | "rule", { events: number; detected: number; event_recall: number }> };
      holdout: { model: DetectionScore; rule: DetectionScore; events: Record<"model" | "rule", { events: number; detected: number; event_recall: number }> };
    }
  >;
  data: { train_seeds: number[]; holdout_seeds: number[] };
  training_seconds: number;
  created_at: string;
  notes: string;
}

export interface ForecastPoint {
  issued_tick: number;
  target_tick: number;
  horizon: number;
  value: number;
}

// ---- Scenarios (Phase 3) ----

export type ScenarioKind = "road_closure" | "rush_hour" | "signal_timing" | "industrial_spike" | "heatwave";

export interface ScenarioParams {
  road_ids?: string[];
  demand_pct?: number;
  policy?: "adaptive";
  district_id?: string;
  emission_factor?: number;
  temp_delta_c?: number;
}

export interface BranchSummary {
  trips_completed: number;
  avg_trip_min: number;
  avg_congestion: number;
  peak_congestion: number;
  avg_speed_kmh: number;
  avg_vehicles: number;
  energy_mwh: number;
  peak_demand_mw: number;
  max_utilization: number;
  overload_minutes: number;
  avg_pm25: number;
  max_station_pm25: number;
  avg_eta_min: number;
  alerts_opened: number;
  rerouted: number;
  stranded: number;
  avg_temp_c: number;
}

export interface ScenarioPoint {
  tick: number;
  congestion: number;
  speed_kmh: number;
  vehicles: number;
  demand_mw: number;
  pm25: number;
  trip_min_1h: number;
}

export interface ScenarioForecasts {
  traffic?: {
    version: string;
    baseline: Record<string, number>;
    scenario: Record<string, number>;
    top_roads: { id: string; baseline: number; scenario: number; delta: number }[];
  };
  energy?: {
    version: string;
    baseline_mwh_24h: number;
    scenario_mwh_24h: number;
    baseline_peak_mw: number;
    scenario_peak_mw: number;
    baseline_city_mw: number[];
    scenario_city_mw: number[];
  };
  pm25?: {
    version: string;
    baseline: Record<string, number>;
    scenario: Record<string, number>;
    max_baseline: Record<string, number>;
    max_scenario: Record<string, number>;
  };
  errors?: Record<string, string>;
}

export interface ScenarioResult {
  spec_hash: string;
  result_hash: string;
  start_time: string;
  end_time: string;
  baseline: BranchSummary;
  scenario: BranchSummary;
  series: { baseline: ScenarioPoint[]; scenario: ScenarioPoint[] };
  roads: { id: string; baseline: number; scenario: number; delta: number; closed?: boolean }[];
  districts: {
    id: string;
    demand_baseline_mw: number;
    demand_scenario_mw: number;
    peak_util_baseline: number;
    peak_util_scenario: number;
    pm25_baseline: number;
    pm25_scenario: number;
  }[];
  pm_grid_delta: number[];
  signal_green_ns?: number[];
  forecasts?: ScenarioForecasts;
  context?: {
    radius_m: number;
    nearby_roads?: { id: string; distance_m: number; delta: number }[];
    nearby_mean_delta?: number;
    nearby_stations?: { id: string; distance_m: number }[];
  };
  fast_forward_s: number;
  run_s: number;
  note: string;
}

export interface Scenario {
  id: string;
  name: string;
  kind: ScenarioKind;
  params: ScenarioParams;
  seed: number;
  base_run_id: string | null;
  base_tick: number;
  duration_min: number;
  spec_hash: string;
  status: "queued" | "running" | "done" | "failed";
  progress: number;
  phase: string | null;
  error: string | null;
  worker: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  result_hash?: string | null;
  /** Full result from GET /api/scenarios/{id}; only baseline/scenario summaries in the list. */
  result?: ScenarioResult | { baseline: BranchSummary; scenario: BranchSummary } | null;
}

export interface ScenarioUpdate {
  id: string;
  status: Scenario["status"];
  phase?: string;
  progress: number;
  error?: string;
  result_hash?: string;
  worker?: string;
}

// ------------------------------------------------------- City Intelligence

export interface AssistantStatus {
  mode: "llm" | "rules";
  model: string | null;
  fallback_model: string | null;
  endpoint: string | null;
  gateway_ok: boolean;
  sim_time: string | null;
}

export interface ToolTrace {
  name: string;
  args: Record<string, unknown>;
  ok: boolean;
  result: Record<string, unknown>;
}

export interface ChatResponse {
  answer: string;
  mode: "llm" | "rules";
  model: string | null;
  note: string | null;
  sim_time: string | null;
  tool_calls: ToolTrace[];
  grounding: { checked: number; ungrounded: string[] };
  elapsed_ms: number;
}

export interface ChatTurn {
  role: "user" | "assistant";
  content: string;
}

// ------------------------------------------------------- Infrastructure (Phase 5)

export interface InfraWorkload {
  kind: "Deployment" | "StatefulSet";
  name: string;
  component: string;
  desired: number;
  ready: number;
  available: number;
  updated: number;
  image?: string;
}

export interface InfraPod {
  name: string;
  component: string;
  phase: string;
  status: string;
  ready: boolean;
  restarts: number;
  node?: string;
  started_at?: string;
  cpu_millicores?: number;
  memory_mib?: number;
  cpu_request_millicores: number;
  cpu_limit_millicores: number;
  memory_request_mib: number;
  memory_limit_mib: number;
}

export interface InfraAutoscaler {
  name: string;
  target: string;
  min: number;
  max: number;
  current?: number;
  desired?: number;
  active?: boolean;
  ready?: boolean;
  trigger: string;
  trigger_metadata?: Record<string, string>;
  last_scale_time?: string;
}

export interface InfraInfo {
  mode: "kubernetes" | "outside-kubernetes";
  namespace?: string;
  checked_at: string;
  error?: string;
  workloads: InfraWorkload[];
  pods: InfraPod[];
  autoscalers: InfraAutoscaler[];
  usage_source: "metrics-server" | "prometheus" | "none";
  prometheus: { configured: boolean; ok: boolean; error?: string };
  grafana_url?: string;
  missing?: string[];
}

export interface InfraSeries {
  labels: Record<string, string>;
  points: [number, number][];
}

export interface InfraSeriesResponse {
  step_s: number;
  start: number;
  end: number;
  series: Partial<Record<
    | "queue_waiting" | "queue_running" | "scenario_workers" | "scenarios_per_min"
    | "gateway_rps" | "sim_steps_per_s" | "cpu_by_component" | "memory_by_component",
    InfraSeries[]
  >>;
  errors?: string[];
}
