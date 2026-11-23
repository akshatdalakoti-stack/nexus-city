package scenarioworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/scenario"
)

// modelTelemetry is the part of a telemetry payload the models read
// (nexus_ml.grid.Sample.from_payload); the rest is dropped to keep
// requests small.
type modelTelemetry struct {
	SimTime   time.Time            `json:"sim_time"`
	Tick      int64                `json:"tick"`
	Weather   events.Weather       `json:"weather"`
	Summary   events.Summary       `json:"summary"`
	Roads     []events.RoadObs     `json:"roads"`
	Districts []events.DistrictObs `json:"districts"`
	Stations  []events.StationObs  `json:"stations"`
}

func trim(ts []*events.Telemetry) []modelTelemetry {
	out := make([]modelTelemetry, len(ts))
	for i, t := range ts {
		out[i] = modelTelemetry{t.SimTime, t.Tick, t.Weather, t.Summary, t.Roads, t.Districts, t.Stations}
	}
	return out
}

func (w *Worker) predict(ctx context.Context, model string, c *city.City, history []*events.Telemetry) (*events.Forecast, error) {
	base, ok := w.cfg.ModelURLs[model]
	if !ok {
		return nil, fmt.Errorf("no URL configured for the %s model", model)
	}
	body, err := json.Marshal(map[string]any{"city": c, "telemetry": trim(history)})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/predict/"+model, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := w.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return nil, fmt.Errorf("%s: %s", res.Status, msg)
	}
	var f events.Forecast
	if err := json.NewDecoder(res.Body).Decode(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func r3(v float64) float64 { return math.Round(v*1000) / 1000 }

// ForecastComparison summarises what each model expects after the scenario
// window, in the baseline branch and in the scenario branch.
type ForecastComparison struct {
	Traffic *TrafficForecast  `json:"traffic,omitempty"`
	Energy  *EnergyForecast   `json:"energy,omitempty"`
	PM25    *PM25Forecast     `json:"pm25,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
}

type TrafficForecast struct {
	Version  string             `json:"version"`
	Baseline map[string]float64 `json:"baseline"` // horizon (min) -> mean predicted congestion
	Scenario map[string]float64 `json:"scenario"`
	TopRoads []RoadForecast     `json:"top_roads"` // largest predicted increase at +30 min
}

type RoadForecast struct {
	ID       string  `json:"id"`
	Baseline float64 `json:"baseline"`
	Scenario float64 `json:"scenario"`
	Delta    float64 `json:"delta"`
}

type EnergyForecast struct {
	Version      string    `json:"version"`
	BaselineMWh  float64   `json:"baseline_mwh_24h"`
	ScenarioMWh  float64   `json:"scenario_mwh_24h"`
	BaselinePeak float64   `json:"baseline_peak_mw"`
	ScenarioPeak float64   `json:"scenario_peak_mw"`
	BaselineCity []float64 `json:"baseline_city_mw"` // city total for +1…+24 h
	ScenarioCity []float64 `json:"scenario_city_mw"`
}

type PM25Forecast struct {
	Version     string             `json:"version"`
	Baseline    map[string]float64 `json:"baseline"` // horizon (min) -> mean predicted station PM2.5
	Scenario    map[string]float64 `json:"scenario"`
	MaxBaseline map[string]float64 `json:"max_baseline"`
	MaxScenario map[string]float64 `json:"max_scenario"`
}

func horizonMeans(f *events.Forecast) map[string]float64 {
	out := map[string]float64{}
	for h, v := range f.Values {
		out[h] = r3(mean(v))
	}
	return out
}

func horizonMax(f *events.Forecast) map[string]float64 {
	out := map[string]float64{}
	for h, v := range f.Values {
		m := 0.0
		for _, x := range v {
			m = math.Max(m, x)
		}
		out[h] = r3(m)
	}
	return out
}

func cityTotals(f *events.Forecast) []float64 {
	out := make([]float64, len(f.Horizons))
	for i, h := range f.Horizons {
		s := 0.0
		for _, x := range f.Values[fmt.Sprint(h)] {
			s += x
		}
		out[i] = math.Round(s*10) / 10
	}
	return out
}

// compareForecasts asks each model about both branches. A model that is not
// reachable is reported in Errors; the scenario still completes.
func (w *Worker) compareForecasts(ctx context.Context, c *city.City, b *scenario.Branches) ForecastComparison {
	out := ForecastComparison{Errors: map[string]string{}}
	pair := func(model string) (*events.Forecast, *events.Forecast, bool) {
		fb, err := w.predict(ctx, model, c, b.Baseline)
		if err == nil {
			var fs *events.Forecast
			if fs, err = w.predict(ctx, model, c, b.Scenario); err == nil {
				return fb, fs, true
			}
		}
		out.Errors[model] = err.Error()
		return nil, nil, false
	}
	if fb, fs, ok := pair("traffic"); ok {
		t := &TrafficForecast{Version: fb.Version, Baseline: horizonMeans(fb), Scenario: horizonMeans(fs)}
		vb, vs := fb.Values["30"], fs.Values["30"]
		for i, id := range fb.Entities {
			if i < len(vb) && i < len(vs) {
				t.TopRoads = append(t.TopRoads, RoadForecast{ID: id, Baseline: r3(vb[i]), Scenario: r3(vs[i]), Delta: r3(vs[i] - vb[i])})
			}
		}
		sort.Slice(t.TopRoads, func(i, j int) bool { return t.TopRoads[i].Delta > t.TopRoads[j].Delta })
		t.TopRoads = t.TopRoads[:min(8, len(t.TopRoads))]
		out.Traffic = t
	}
	if fb, fs, ok := pair("energy"); ok {
		cb, cs := cityTotals(fb), cityTotals(fs)
		e := &EnergyForecast{Version: fb.Version, BaselineCity: cb, ScenarioCity: cs}
		for i := range cb {
			e.BaselineMWh += cb[i]
			e.ScenarioMWh += cs[i]
			e.BaselinePeak = math.Max(e.BaselinePeak, cb[i])
			e.ScenarioPeak = math.Max(e.ScenarioPeak, cs[i])
		}
		e.BaselineMWh, e.ScenarioMWh = math.Round(e.BaselineMWh), math.Round(e.ScenarioMWh)
		out.Energy = e
	}
	if fb, fs, ok := pair("pm25"); ok {
		out.PM25 = &PM25Forecast{Version: fb.Version, Baseline: horizonMeans(fb), Scenario: horizonMeans(fs),
			MaxBaseline: horizonMax(fb), MaxScenario: horizonMax(fs)}
	}
	if len(out.Errors) == 0 {
		out.Errors = nil
	}
	return out
}
