package sim

import (
	"fmt"
	"math"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

// ---- Energy ----

type energyState struct {
	ar     float64 // AR(1) noise term
	demand float64 // MW
}

func (e *Engine) initEnergy() {
	e.energy = make([]energyState, len(e.City.Districts))
}

// LoadProfile is the demand multiplier on a district's base load by hour.
func LoadProfile(typ string, h float64, weekend bool) float64 {
	res := 0.62 + 0.22*bump(h, 7.5, 1.2) + 0.55*bump(h, 20.3, 2.0) + 0.12*bump(h, 13, 2.5)
	com := 0.45 + 0.85*plateau(h, 8.5, 19)
	ind := 0.7 + 0.45*plateau(h, 6, 22)
	if weekend {
		res += 0.1 * bump(h, 11, 3)
		com = 0.45 + 0.45*plateau(h, 10, 18)
		ind *= 0.85
	}
	switch typ {
	case city.Residential:
		return res
	case city.Commercial:
		return com
	case city.Industrial:
		return ind
	default:
		return (res + com) / 2
	}
}

// TemperatureFactor adds cooling load above 22 °C and heating below 12 °C.
func TemperatureFactor(tempC float64) float64 {
	return 1 + 0.03*math.Max(0, tempC-22) + 0.015*math.Max(0, 12-tempC)
}

func (e *Engine) updateEnergy() {
	t := e.SimTime()
	h, we := hourOf(t), isWeekend(t)
	tf := TemperatureFactor(e.weather.TempC)
	for i, d := range e.City.Districts {
		s := &e.energy[i]
		s.ar = 0.9*s.ar + 0.012*e.rEnergy.NormFloat64()
		s.demand = d.BaseLoadMW * LoadProfile(d.Type, h, we) * tf * (1 + s.ar) * e.surgeFactor(i)
	}
}

// ---- Incidents ----

type incident struct {
	id       int
	road     int
	severity string
	start    int64
	end      int64
}

func incidentID(id int) string { return fmt.Sprintf("INC-%d", id) }

func (e *Engine) updateIncidents() {
	// Clear finished incidents.
	kept := e.incidents[:0]
	for _, in := range e.incidents {
		if e.Tick >= in.end {
			rs := &e.roads[in.road]
			rs.laneFactor, rs.incident = 1, 0
			road := e.City.Roads[in.road]
			e.emit(events.Event{Kind: "incident.cleared", Severity: "info", EntityType: "road", EntityID: road.ID,
				Message: fmt.Sprintf("%s cleared on %s (%s)", incidentID(in.id), road.Name, road.Segment)})
			continue
		}
		kept = append(kept, in)
	}
	e.incidents = kept

	// New incidents: more traffic, more incidents.
	lambda := 0.002 + 0.000018*float64(len(e.vehicles))
	for n := poisson(e.rIncident, lambda); n > 0; n-- {
		var cand []int
		var w []float64
		for i, road := range e.City.Roads {
			if road.Class == city.Local || e.roads[i].incident != 0 {
				continue
			}
			cand = append(cand, i)
			w = append(w, float64(e.roads[i].count[0]+e.roads[i].count[1])+1)
		}
		if len(cand) == 0 {
			return
		}
		ri := cand[weightedPick(e.rIncident, w)]
		severity, factor, dur := "minor", 0.67, 20+e.rIncident.IntN(41)
		if e.rIncident.Float64() < 0.3 {
			severity, factor, dur = "major", 0.34, 40+e.rIncident.IntN(41)
		}
		e.incSeq++
		in := &incident{id: e.incSeq, road: ri, severity: severity, start: e.Tick, end: e.Tick + int64(dur)*60}
		e.incidents = append(e.incidents, in)
		e.roads[ri].laneFactor, e.roads[ri].incident = factor, in.id
		road := e.City.Roads[ri]
		e.emit(events.Event{Kind: "incident.started", Severity: severityFor(severity), EntityType: "road", EntityID: road.ID,
			Message: fmt.Sprintf("%s incident on %s (%s)", capitalize(severity), road.Name, road.Segment),
			Data:    map[string]any{"incident_id": incidentID(in.id), "expected_minutes": dur}})
	}
}

func severityFor(s string) string {
	if s == "major" {
		return "critical"
	}
	return "warning"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

// ---- Emergency services ----

type call struct {
	id       int
	tick     int64
	district int
	hospital int // index into e.hospitals
	etaS     float64
}

const dispatchSeconds = 90

func (e *Engine) updateEmergency() {
	kept := e.calls[:0]
	for _, c := range e.calls {
		if e.Tick-c.tick < 3600 {
			kept = append(kept, c)
		}
	}
	e.calls = kept

	incidentsIn := make([]int, len(e.City.Districts))
	for _, in := range e.incidents {
		incidentsIn[e.City.Roads[in.road].District]++
	}
	for di, d := range e.City.Districts {
		lambda := float64(d.Population)/100000*5/60 + 0.08*float64(incidentsIn[di])
		for n := poisson(e.rEmergency, lambda); n > 0; n-- {
			node := e.pickNode(e.rEmergency, di)
			times := e.City.TravelTimes(node, e.roadCost, e.signalPenalty)
			best, bestT := 0, math.Inf(1)
			for hi, fi := range e.hospitals {
				if t := times[e.City.Facilities[fi].Node]; t < bestT {
					best, bestT = hi, t
				}
			}
			e.callSeq++
			c := call{id: e.callSeq, tick: e.Tick, district: di, hospital: best, etaS: bestT + dispatchSeconds}
			e.calls = append(e.calls, c)
			h := e.City.Facilities[e.hospitals[best]]
			e.emit(events.Event{Kind: "emergency.call", Severity: "info", EntityType: "district", EntityID: d.ID,
				Message: fmt.Sprintf("Emergency call in %s → %s, ETA %.1f min", d.Name, h.Name, c.etaS/60),
				Data:    map[string]any{"call_id": c.id, "hospital_id": h.ID, "eta_min": round1(c.etaS / 60)}})
		}
	}
}
