package gateway

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"sync"

	"nexuscity/internal/events"
)

// mlState holds model outputs for the current run and scores forecasts
// against what the simulation then actually did. The live city is never
// used for training, so this is a genuine out-of-sample check.
type mlState struct {
	mu        sync.RWMutex
	forecasts map[string][]storedForecast // model -> recent forecasts, oldest first
	latest    map[string][]byte           // model -> latest forecast envelope
	anomalies []byte                      // latest anomaly envelope
	accuracy  map[string]map[int][]accuracyPoint
	maxKeep   int
}

type storedForecast struct {
	tick int64
	f    *events.Forecast
	idx  map[string]int
}

type accuracyPoint struct {
	Tick        int64   `json:"tick"`
	ModelMAE    float64 `json:"model_mae"`
	BaselineMAE float64 `json:"baseline_mae"`
	N           int     `json:"n"`
}

func newMLState() *mlState {
	return &mlState{forecasts: map[string][]storedForecast{}, latest: map[string][]byte{},
		accuracy: map[string]map[int][]accuracyPoint{}, maxKeep: 400}
}

func (m *mlState) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forecasts, m.latest, m.anomalies = map[string][]storedForecast{}, map[string][]byte{}, nil
	m.accuracy = map[string]map[int][]accuracyPoint{}
}

func (m *mlState) addForecast(env events.Envelope, raw []byte) error {
	var f events.Forecast
	if err := json.Unmarshal(env.Payload, &f); err != nil {
		return err
	}
	idx := make(map[string]int, len(f.Entities))
	for i, id := range f.Entities {
		idx[id] = i
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	list := append(m.forecasts[f.Model], storedForecast{tick: f.IssuedTick, f: &f, idx: idx})
	if over := len(list) - m.maxKeep; over > 0 {
		list = append([]storedForecast(nil), list[over:]...)
	}
	m.forecasts[f.Model] = list
	m.latest[f.Model] = raw
	return nil
}

func (m *mlState) setAnomalies(raw []byte) {
	m.mu.Lock()
	m.anomalies = raw
	m.mu.Unlock()
}

// actuals extracts the values a model forecasts from a telemetry snapshot.
// Faulty air-quality readings are NaN and excluded from scoring.
func actuals(model string, t *events.Telemetry) map[string]float64 {
	out := map[string]float64{}
	switch model {
	case "traffic":
		for _, o := range t.Roads {
			out[o.ID] = o.Congestion
		}
	case "energy":
		for _, o := range t.Districts {
			out[o.ID] = o.DemandMW
		}
	case "pm25":
		for _, o := range t.Stations {
			if o.Status == "ok" && o.PM25 != nil {
				out[o.ID] = *o.PM25
			} else {
				out[o.ID] = math.NaN()
			}
		}
	}
	return out
}

// score compares every stored forecast that targeted this telemetry's tick
// with the actual values, alongside the naive baseline the model was
// benchmarked against in training: persistence (the value when the
// forecast was issued) for traffic and PM2.5, and the value 24 h before the
// target for energy.
func (m *mlState) score(t *events.Telemetry, lookup func(tick int64) *events.Telemetry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for model, list := range m.forecasts {
		act := actuals(model, t)
		for _, sf := range list {
			for hi, target := range sf.f.TargetTicks {
				if target != t.Tick {
					continue
				}
				var baseRef *events.Telemetry
				if model == "energy" {
					baseRef = lookup(target - 86400)
				} else {
					baseRef = lookup(sf.tick)
				}
				var base map[string]float64
				if baseRef != nil {
					base = actuals(model, baseRef)
				}
				h := sf.f.Horizons[hi]
				vals := sf.f.Values[strconv.Itoa(h)]
				var sumM, sumB float64
				n, nb := 0, 0
				for id, i := range sf.idx {
					a, ok := act[id]
					if !ok || math.IsNaN(a) || i >= len(vals) {
						continue
					}
					sumM += math.Abs(vals[i] - a)
					n++
					if b, ok := base[id]; ok && !math.IsNaN(b) {
						sumB += math.Abs(b - a)
						nb++
					}
				}
				if n == 0 {
					continue
				}
				p := accuracyPoint{Tick: t.Tick, ModelMAE: sumM / float64(n), BaselineMAE: math.NaN(), N: n}
				if nb == n {
					p.BaselineMAE = sumB / float64(nb)
				}
				if m.accuracy[model] == nil {
					m.accuracy[model] = map[int][]accuracyPoint{}
				}
				pts := append(m.accuracy[model][h], p)
				if over := len(pts) - 600; over > 0 {
					pts = pts[over:]
				}
				m.accuracy[model][h] = pts
			}
		}
	}
}

// AccuracySummary is the live error over a recent window, per horizon.
type AccuracySummary struct {
	Horizon     int      `json:"horizon"`
	ModelMAE    float64  `json:"model_mae"`
	BaselineMAE *float64 `json:"baseline_mae"`
	Points      int      `json:"points"`
	FromTick    int64    `json:"from_tick"`
	ToTick      int64    `json:"to_tick"`
}

func (m *mlState) accuracySummary(model string, windowS int64) []AccuracySummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	byH := m.accuracy[model]
	hs := make([]int, 0, len(byH))
	for h := range byH {
		hs = append(hs, h)
	}
	sort.Ints(hs)
	out := []AccuracySummary{}
	for _, h := range hs {
		pts := byH[h]
		if len(pts) == 0 {
			continue
		}
		last := pts[len(pts)-1].Tick
		var sm, sb float64
		var nm, nb int
		s := AccuracySummary{Horizon: h, ToTick: last, FromTick: last}
		baseOK := true
		for _, p := range pts {
			if p.Tick < last-windowS {
				continue
			}
			if p.Tick < s.FromTick {
				s.FromTick = p.Tick
			}
			sm += p.ModelMAE * float64(p.N)
			nm += p.N
			if math.IsNaN(p.BaselineMAE) {
				baseOK = false
			} else {
				sb += p.BaselineMAE * float64(p.N)
				nb += p.N
			}
			s.Points++
		}
		if nm == 0 {
			continue
		}
		s.ModelMAE = sm / float64(nm)
		if baseOK && nb > 0 {
			v := sb / float64(nb)
			s.BaselineMAE = &v
		}
		out = append(out, s)
	}
	return out
}

// ForecastPoint is one past forecast for one entity.
type ForecastPoint struct {
	IssuedTick int64   `json:"issued_tick"`
	TargetTick int64   `json:"target_tick"`
	Horizon    int     `json:"horizon"`
	Value      float64 `json:"value"`
}

func (m *mlState) entityForecasts(model, id string, sinceTick int64) ([]ForecastPoint, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list, ok := m.forecasts[model]
	if !ok {
		return nil, false
	}
	out := []ForecastPoint{}
	found := false
	for _, sf := range list {
		i, ok := sf.idx[id]
		if !ok {
			continue
		}
		found = true
		if sf.tick < sinceTick {
			continue
		}
		for hi, h := range sf.f.Horizons {
			vals := sf.f.Values[strconv.Itoa(h)]
			if i < len(vals) {
				out = append(out, ForecastPoint{IssuedTick: sf.tick, TargetTick: sf.f.TargetTicks[hi], Horizon: h, Value: vals[i]})
			}
		}
	}
	return out, found || len(list) == 0
}

func (m *mlState) latestForecasts() map[string]json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(m.latest))
	for k, v := range m.latest {
		out[k] = v
	}
	return out
}

func (m *mlState) latestAnomalies() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.anomalies
}
