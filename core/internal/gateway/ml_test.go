package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"nexuscity/internal/bus"
	"nexuscity/internal/events"
)

func roadTelemetry(tick int64, cong map[string]float64) *events.Telemetry {
	t := &events.Telemetry{Tick: tick}
	for _, id := range []string{"R1", "R2"} {
		t.Roads = append(t.Roads, events.RoadObs{ID: id, Congestion: cong[id]})
	}
	return t
}

// A forecast is scored when its target tick arrives, against the actual
// value and against persistence (the value when it was issued).
func TestForecastScoring(t *testing.T) {
	m := newMLState()
	f := events.Forecast{Model: "traffic", EntityType: "road", Horizons: []int{15}, TargetTicks: []int64{900 + 900},
		IssuedTick: 900, Entities: []string{"R1", "R2"}, Values: map[string][]float64{"15": {0.5, 0.2}}}
	payload, _ := json.Marshal(f)
	if err := m.addForecast(events.Envelope{Payload: payload}, payload); err != nil {
		t.Fatal(err)
	}
	history := map[int64]*events.Telemetry{900: roadTelemetry(900, map[string]float64{"R1": 0.1, "R2": 0.1})}
	lookup := func(tick int64) *events.Telemetry { return history[tick] }

	m.score(roadTelemetry(1200, map[string]float64{"R1": 0.9}), lookup) // not the target tick
	if len(m.accuracySummary("traffic", 3600)) != 0 {
		t.Fatal("scored a forecast before its target time")
	}
	m.score(roadTelemetry(1800, map[string]float64{"R1": 0.6, "R2": 0.3}), lookup)
	s := m.accuracySummary("traffic", 3600)
	if len(s) != 1 || s[0].Horizon != 15 {
		t.Fatalf("summary %+v", s)
	}
	// model errors |0.5-0.6|, |0.2-0.3| -> 0.1; persistence |0.1-0.6|, |0.1-0.3| -> 0.35
	if math.Abs(s[0].ModelMAE-0.1) > 1e-9 || s[0].BaselineMAE == nil || math.Abs(*s[0].BaselineMAE-0.35) > 1e-9 {
		t.Fatalf("model %.3f baseline %v", s[0].ModelMAE, s[0].BaselineMAE)
	}
}

func TestFaultySensorsAreNotScored(t *testing.T) {
	m := newMLState()
	f := events.Forecast{Model: "pm25", EntityType: "station", Horizons: []int{30}, TargetTicks: []int64{1800},
		IssuedTick: 0, Entities: []string{"AQ1", "AQ2"}, Values: map[string][]float64{"30": {20, 30}}}
	payload, _ := json.Marshal(f)
	_ = m.addForecast(events.Envelope{Payload: payload}, payload)
	v := 25.0
	tel := &events.Telemetry{Tick: 1800, Stations: []events.StationObs{{ID: "AQ1", PM25: &v, Status: "ok"}, {ID: "AQ2", Status: "offline"}}}
	m.score(tel, func(int64) *events.Telemetry { return nil })
	s := m.accuracySummary("pm25", 3600)
	if len(s) != 1 || s[0].ModelMAE != 5 {
		t.Fatalf("expected only the healthy sensor scored (MAE 5), got %+v", s)
	}
}

func TestForecastStreamReachesAPIAndWebSocketState(t *testing.T) {
	h := newHarness(t)
	h.advance(3)
	f := events.Forecast{Model: "traffic", Version: "traffic-test", EntityType: "road", HorizonUnit: "min",
		Horizons: []int{15}, TargetTicks: []int64{900}, IssuedTick: 0, Entities: []string{"R1"},
		Values: map[string][]float64{"15": {0.4}}}
	payload, _ := json.Marshal(f)
	env := events.Envelope{Schema: events.SchemaForecast, RunID: h.worker.RunID(), Seed: 42, Tick: 0,
		SimTime: time.Date(2026, 6, 1, 5, 30, 0, 0, time.UTC), EmittedAt: time.Now().UTC(), Payload: payload}
	if _, err := bus.Publish(h.ctx, h.rdb, events.StreamForecasts, env); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, func() bool { _, ok := h.srv.ml.latestForecasts()["traffic"]; return ok })

	code, body := h.do(t, "GET", "/api/forecasts/traffic/R1?minutes=60", "")
	var res struct {
		Points []ForecastPoint `json:"points"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &res) != nil || len(res.Points) != 1 || res.Points[0].Value != 0.4 {
		t.Fatalf("entity forecasts: %d %s", code, body)
	}
	if code, _ := h.do(t, "GET", "/api/forecasts/weather/R1", ""); code != http.StatusBadRequest {
		t.Fatalf("unknown model accepted: %d", code)
	}
	// Model registry entries come from the services via Redis.
	h.rdb.HSet(h.ctx, events.KeyModels, "traffic", `{"kind":"traffic","version":"traffic-test","updated_at":"`+time.Now().UTC().Format(time.RFC3339Nano)+`"}`)
	code, body = h.do(t, "GET", "/api/models", "")
	var models []ModelStatus
	if code != http.StatusOK || json.Unmarshal([]byte(body), &models) != nil || len(models) != 4 || !models[0].Alive || models[1].Alive {
		t.Fatalf("models: %d %s", code, body)
	}
	if code, _ := h.do(t, "GET", "/api/models/energy", ""); code != http.StatusNotFound {
		t.Fatalf("unserved model metadata: %d", code)
	}
}
