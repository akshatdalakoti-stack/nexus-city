// Package events defines the wire contract shared by the simulation worker,
// the gateway and (via docs/events.md and web/src/lib/types.ts) the frontend
// and Python services. Every message on a Redis stream is an Envelope.
package events

import (
	"encoding/json"
	"fmt"
	"time"
)

// Redis stream and key names.
const (
	StreamFrames    = "nexus:frames"    // vehicle positions, ~5 Hz wall clock
	StreamTelemetry = "nexus:telemetry" // full city observation, once per simulated minute
	StreamEvents    = "nexus:events"    // discrete events: incidents, calls, alerts, sensor faults
	StreamClock     = "nexus:clock"     // clock changes and periodic clock beats
	StreamControl   = "nexus:control"   // commands to the simulation worker
	StreamForecasts = "nexus:forecasts" // model forecasts (Python inference services)

	StreamScenarios       = "nexus:scenarios"        // queue of scenario ids (consumer group below)
	StreamScenarioUpdates = "nexus:scenario_updates" // scenario status and progress
	GroupScenarioWorkers  = "scenario-workers"
	StreamAnomalies       = "nexus:anomalies"   // anomaly scores (Python anomaly service)
	KeyModels             = "nexus:models"      // hash: model kind -> live service info (JSON)
	KeyModelMeta          = "nexus:model_meta:" // + kind: training metadata JSON of the served version

	KeyCity      = "nexus:city"          // current city JSON
	KeyClock     = "nexus:clock:latest"  // latest clock envelope
	KeyHeartbeat = "nexus:sim:heartbeat" // hash, expires if the worker dies
)

// Schema identifiers carried in Envelope.Schema.
const (
	SchemaFrame     = "nexus.frame/v1"
	SchemaTelemetry = "nexus.telemetry/v1"
	SchemaEvent     = "nexus.event/v1"
	SchemaClock     = "nexus.clock/v1"
	SchemaForecast  = "nexus.forecast/v1"
	SchemaAnomaly   = "nexus.anomaly/v1"
	SchemaScenario  = "nexus.scenario/v1"
)

// ScenarioUpdate is published whenever a scenario's status or progress changes.
type ScenarioUpdate struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"` // queued | running | done | failed
	Phase      string  `json:"phase,omitempty"`
	Progress   float64 `json:"progress"`
	Error      string  `json:"error,omitempty"`
	ResultHash string  `json:"result_hash,omitempty"`
	Worker     string  `json:"worker,omitempty"`
}

// Forecast is published by a model service (Python) after each prediction
// round. Values[h][i] is the forecast for Entities[i] at horizon h.
type Forecast struct {
	Model       string               `json:"model"` // traffic | energy | pm25
	Version     string               `json:"version"`
	EntityType  string               `json:"entity_type"` // road | district | station
	HorizonUnit string               `json:"horizon_unit"`
	Horizons    []int                `json:"horizons"`
	TargetTicks []int64              `json:"target_ticks"`
	OutputUnit  string               `json:"output_unit"`
	IssuedTick  int64                `json:"issued_tick"`
	Entities    []string             `json:"entities"`
	Values      map[string][]float64 `json:"values"`
	LatencyMs   float64              `json:"latency_ms"`
}

// AnomalyScore is one entity's anomaly score.
type AnomalyScore struct {
	EntityType string  `json:"entity_type"`
	EntityID   string  `json:"entity_id"`
	Score      float64 `json:"score"`
	Anomalous  bool    `json:"anomalous"`
	Reason     string  `json:"reason"`
}

// Anomalies is published by the anomaly service every 5 simulated minutes.
type Anomalies struct {
	Model      string                    `json:"model"`
	Version    string                    `json:"version"`
	IssuedTick int64                     `json:"issued_tick"`
	Flagged    []AnomalyScore            `json:"flagged"`
	Top        map[string][]AnomalyScore `json:"top"`
	LatencyMs  float64                   `json:"latency_ms"`
}

// Envelope wraps every published message.
type Envelope struct {
	Schema    string          `json:"schema"`
	RunID     string          `json:"run_id"`
	Seed      int64           `json:"seed"`
	Tick      int64           `json:"tick"` // simulated seconds since the run's epoch
	SimTime   time.Time       `json:"sim_time"`
	EmittedAt time.Time       `json:"emitted_at"` // wall clock; excluded from determinism checks
	Payload   json.RawMessage `json:"payload"`
}

// Clock is the shared simulation clock.
type Clock struct {
	RunID   string    `json:"run_id"`
	Seed    int64     `json:"seed"`
	Epoch   time.Time `json:"epoch"`
	SimTime time.Time `json:"sim_time"`
	Tick    int64     `json:"tick"`
	Speed   float64   `json:"speed"`
	Paused  bool      `json:"paused"`
}

// AllowedSpeeds are the simulation speeds a client may request.
// 60× is an extra demo speed beyond the 1/2/5/10× in the brief.
var AllowedSpeeds = []float64{1, 2, 5, 10, 60}

// Control is a command for the simulation worker.
type Control struct {
	Action string  `json:"action"` // pause | resume | speed | reset
	Speed  float64 `json:"speed,omitempty"`
	Seed   *int64  `json:"seed,omitempty"`
}

// Validate checks a control command before it is queued.
func (c Control) Validate() error {
	switch c.Action {
	case "pause", "resume":
		return nil
	case "speed":
		for _, s := range AllowedSpeeds {
			if c.Speed == s {
				return nil
			}
		}
		return fmt.Errorf("speed must be one of %v", AllowedSpeeds)
	case "reset":
		if c.Seed != nil && (*c.Seed < 0 || *c.Seed > 1<<31) {
			return fmt.Errorf("seed must be between 0 and %d", int64(1<<31))
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q (want pause, resume, speed or reset)", c.Action)
	}
}

// Frame carries vehicle positions for animation.
type Frame struct {
	SimTime time.Time `json:"sim_time"`
	Tick    int64     `json:"tick"`
	Scale   float64   `json:"scale"` // real vehicles each agent stands for
	// Each vehicle is [id, lon, lat, heading_deg, speed_kmh].
	Vehicles [][5]float64 `json:"vehicles"`
}

// Weather is the simulated weather.
type Weather struct {
	TempC      float64 `json:"temp_c"`
	Humidity   float64 `json:"humidity"`
	WindSpeed  float64 `json:"wind_speed_ms"`
	WindDirDeg float64 `json:"wind_dir_deg"` // direction the wind blows from
}

// RoadObs is one road segment's minute-averaged traffic observation.
type RoadObs struct {
	ID         string  `json:"id"`
	FlowVph    float64 `json:"flow_vph"`
	SpeedKmh   float64 `json:"speed_kmh"`
	Congestion float64 `json:"congestion"` // 0 free-flow … 1 jammed
	Vehicles   int     `json:"vehicles"`
	Incident   string  `json:"incident,omitempty"`
}

// NodeObs is a signalised intersection's average queue over the minute.
type NodeObs struct {
	ID       string  `json:"id"`
	QueueVeh float64 `json:"queue_veh"`
}

// DistrictObs aggregates one district.
type DistrictObs struct {
	ID          string  `json:"id"`
	DemandMW    float64 `json:"demand_mw"`
	CapacityMW  float64 `json:"capacity_mw"`
	Utilization float64 `json:"utilization"`
	PM25        float64 `json:"pm25"` // mean of the modelled field over the district
	Vehicles    int     `json:"vehicles"`
	Calls1h     int     `json:"calls_1h"`
	AvgEtaMin   float64 `json:"avg_eta_min"`
}

// StationObs is an air-quality sensor reading. PM25 is null when the sensor
// is offline; a stuck sensor repeats its last value.
type StationObs struct {
	ID     string   `json:"id"`
	PM25   *float64 `json:"pm25"`
	Status string   `json:"status"` // ok | stuck | offline
}

// HospitalObs is a hospital's emergency load.
type HospitalObs struct {
	ID        string  `json:"id"`
	Calls1h   int     `json:"calls_1h"`
	AvgEtaMin float64 `json:"avg_eta_min"`
}

// IncidentObs is an active traffic incident.
type IncidentObs struct {
	ID         string    `json:"id"`
	RoadID     string    `json:"road_id"`
	Severity   string    `json:"severity"`
	StartedAt  time.Time `json:"started_at"`
	ExpectedAt time.Time `json:"expected_clear_at"`
}

// Alert is a rule-based alert. Source is always "rule" in Phase 1; ML
// anomaly alerts arrive in Phase 2 with Source "model".
type Alert struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"`
	Kind       string     `json:"kind"`
	Severity   string     `json:"severity"` // info | warning | critical
	EntityType string     `json:"entity_type"`
	EntityID   string     `json:"entity_id"`
	Title      string     `json:"title"`
	Detail     string     `json:"detail"`
	Value      float64    `json:"value"`
	Source     string     `json:"source"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Summary is the city-wide headline numbers.
type Summary struct {
	Vehicles      int     `json:"vehicles"`
	Agents        int     `json:"agents"`
	AvgSpeedKmh   float64 `json:"avg_speed_kmh"`
	AvgCongestion float64 `json:"avg_congestion"`
	DemandMW      float64 `json:"demand_mw"`
	CapacityMW    float64 `json:"capacity_mw"`
	AvgPM25       float64 `json:"avg_pm25"`
	MaxPM25       float64 `json:"max_pm25"`
	Trips1h       int     `json:"trips_1h"`
	AvgTripMin    float64 `json:"avg_trip_min"`
	Calls1h       int     `json:"calls_1h"`
	AvgEtaMin     float64 `json:"avg_eta_min"`
	Incidents     int     `json:"incidents"`
	Alerts        int     `json:"alerts"`
	SensorFaults  int     `json:"sensor_faults"`
}

// Telemetry is the full observation of the city at one simulated minute.
// Roads, Nodes, Districts, Stations and Hospitals are ordered as in the city.
type Telemetry struct {
	SimTime   time.Time     `json:"sim_time"`
	Tick      int64         `json:"tick"`
	Weather   Weather       `json:"weather"`
	Summary   Summary       `json:"summary"`
	Roads     []RoadObs     `json:"roads"`
	Nodes     []NodeObs     `json:"nodes"`
	Districts []DistrictObs `json:"districts"`
	Stations  []StationObs  `json:"stations"`
	Hospitals []HospitalObs `json:"hospitals"`
	Incidents []IncidentObs `json:"incidents"`
	Alerts    []Alert       `json:"alerts"`
	PMGrid    []float64     `json:"pm_grid"` // row-major, matches city.grid
}

// Event is a discrete happening in the simulation.
type Event struct {
	Kind       string         `json:"kind"` // incident.started, incident.cleared, emergency.call, sensor.fault, sensor.recovered, alert.opened, alert.resolved
	SimTime    time.Time      `json:"sim_time"`
	Severity   string         `json:"severity"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Message    string         `json:"message"`
	Data       map[string]any `json:"data,omitempty"`
}
