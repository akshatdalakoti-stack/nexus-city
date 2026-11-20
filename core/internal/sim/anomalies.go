package sim

import (
	"fmt"

	"nexuscity/internal/events"
)

// Injected anomalies are rare, labelled disturbances. They exist so the
// anomaly detector (Phase 2) can be scored against ground truth:
//   - energy surges: a district's demand jumps 20–40% for 30–120 minutes
//   - emission releases: an industrial stack emits 3–6× for 20–90 minutes
//
// Incidents (traffic) and sensor faults (air quality) are the other labelled
// anomaly types. All of these are announced as events, so the event log
// doubles as the label set. They draw from their own random stream.
const (
	surgeChancePerMin   = 1.0 / (6 * 24 * 60) // per district: about once every 6 simulated days
	releaseChancePerMin = 1.0 / (4 * 24 * 60) // per stack: about once every 4 simulated days
)

type disturbance struct {
	factor float64
	end    int64 // tick; 0 when inactive
}

func (e *Engine) initAnomalies() {
	e.surges = make([]disturbance, len(e.City.Districts))
	e.releases = make([]disturbance, len(e.stacks))
}

func (e *Engine) updateAnomalies() {
	for i := range e.surges {
		d := e.City.Districts[i]
		s := &e.surges[i]
		if s.end != 0 && e.Tick >= s.end {
			*s = disturbance{}
			e.emit(events.Event{Kind: "anomaly.energy_surge.ended", Severity: "info", EntityType: "district", EntityID: d.ID,
				Message: fmt.Sprintf("Injected demand surge in %s ended", d.Name)})
		}
		if s.end == 0 && e.rAnomaly.Float64() < surgeChancePerMin {
			s.factor = 1.2 + 0.2*e.rAnomaly.Float64()
			minutes := 30 + e.rAnomaly.IntN(91)
			s.end = e.Tick + int64(minutes)*60
			e.emit(events.Event{Kind: "anomaly.energy_surge.started", Severity: "info", EntityType: "district", EntityID: d.ID,
				Message: fmt.Sprintf("Injected demand surge in %s (+%.0f%%, %d min) — ground truth for anomaly scoring", d.Name, (s.factor-1)*100, minutes),
				Data:    map[string]any{"factor": round2(s.factor), "minutes": minutes}})
		}
	}
	for i := range e.releases {
		st := e.stacks[i]
		d := e.City.Districts[st.district]
		r := &e.releases[i]
		if r.end != 0 && e.Tick >= r.end {
			*r = disturbance{}
			e.emit(events.Event{Kind: "anomaly.emission_release.ended", Severity: "info", EntityType: "district", EntityID: d.ID,
				Message: fmt.Sprintf("Injected emission release in %s ended", d.Name), Data: map[string]any{"stack": i}})
		}
		if r.end == 0 && e.rAnomaly.Float64() < releaseChancePerMin {
			r.factor = 3 + 3*e.rAnomaly.Float64()
			minutes := 20 + e.rAnomaly.IntN(71)
			r.end = e.Tick + int64(minutes)*60
			e.emit(events.Event{Kind: "anomaly.emission_release.started", Severity: "info", EntityType: "district", EntityID: d.ID,
				Message: fmt.Sprintf("Injected emission release in %s (×%.1f, %d min) — ground truth for anomaly scoring", d.Name, r.factor, minutes),
				Data:    map[string]any{"stack": i, "factor": round2(r.factor), "minutes": minutes}})
		}
	}
}

func (e *Engine) surgeFactor(district int) float64 {
	if s := e.surges[district]; s.end != 0 {
		return s.factor
	}
	return 1
}

func (e *Engine) releaseFactor(stack int) float64 {
	if r := e.releases[stack]; r.end != 0 {
		return r.factor
	}
	return 1
}
