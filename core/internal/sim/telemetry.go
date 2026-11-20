package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

func (e *Engine) buildTelemetry(roads []events.RoadObs) *events.Telemetry {
	c := e.City
	t := &events.Telemetry{SimTime: e.SimTime(), Tick: e.Tick, Weather: e.weather, Roads: roads}

	// Signalised intersections: average queue over the minute.
	steps := float64(max(e.acc.steps, 1))
	for i, n := range c.Nodes {
		if n.Signalized {
			t.Nodes = append(t.Nodes, events.NodeObs{ID: n.ID, QueueVeh: round1(e.acc.queue[i] / steps)})
		}
	}

	// Districts.
	vehIn := make([]int, len(c.Districts))
	for _, v := range e.vehicles {
		vehIn[c.Roads[v.route[v.leg].Road].District]++
	}
	pmSum := make([]float64, len(c.Districts))
	pmN := make([]int, len(c.Districts))
	for i, p := range e.pm {
		pmSum[e.cellDist[i]] += p
		pmN[e.cellDist[i]]++
	}
	calls := make([]int, len(c.Districts))
	etas := make([]float64, len(c.Districts))
	hCalls := make([]int, len(e.hospitals))
	hEtas := make([]float64, len(e.hospitals))
	for _, cl := range e.calls {
		calls[cl.district]++
		etas[cl.district] += cl.etaS
		hCalls[cl.hospital]++
		hEtas[cl.hospital] += cl.etaS
	}
	var demand, capacity float64
	for i, d := range c.Districts {
		dem := e.energy[i].demand
		demand += dem
		capacity += d.SubstationMW
		t.Districts = append(t.Districts, events.DistrictObs{
			ID:          d.ID,
			DemandMW:    round2(dem),
			CapacityMW:  d.SubstationMW,
			Utilization: round3(dem / d.SubstationMW),
			PM25:        round1(pmSum[i] / float64(max(pmN[i], 1))),
			Vehicles:    int(float64(vehIn[i]) * AgentScale),
			Calls1h:     calls[i],
			AvgEtaMin:   avgMin(etas[i], calls[i]),
		})
	}
	for hi, fi := range e.hospitals {
		t.Hospitals = append(t.Hospitals, events.HospitalObs{
			ID: c.Facilities[fi].ID, Calls1h: hCalls[hi], AvgEtaMin: avgMin(hEtas[hi], hCalls[hi])})
	}

	t.Stations = e.stationObs()
	for _, in := range e.incidents {
		t.Incidents = append(t.Incidents, events.IncidentObs{
			ID: incidentID(in.id), RoadID: c.Roads[in.road].ID, Severity: in.severity,
			StartedAt:  Epoch.Add(time.Duration(in.start) * time.Second),
			ExpectedAt: Epoch.Add(time.Duration(in.end) * time.Second),
		})
	}
	t.PMGrid = make([]float64, len(e.pm))
	for i, p := range e.pm {
		t.PMGrid[i] = round1(p)
	}

	// Summary.
	s := &t.Summary
	s.Agents = len(e.vehicles)
	s.Vehicles = int(float64(s.Agents) * AgentScale)
	if s.Agents > 0 {
		sum := 0.0
		for _, v := range e.vehicles {
			sum += v.speed
		}
		s.AvgSpeedKmh = round1(sum / float64(s.Agents) * 3.6)
	}
	var lenSum, congSum float64
	for i, o := range roads {
		l := c.Roads[i].LengthM
		lenSum += l
		congSum += o.Congestion * l
	}
	s.AvgCongestion = round3(congSum / lenSum)
	s.DemandMW = round1(demand)
	s.CapacityMW = round1(capacity)
	var pmTotal float64
	pmCount := 0
	for _, st := range t.Stations {
		if st.Status == "ok" && st.PM25 != nil {
			pmTotal += *st.PM25
			pmCount++
			s.MaxPM25 = math.Max(s.MaxPM25, *st.PM25)
		}
		if st.Status != "ok" {
			s.SensorFaults++
		}
	}
	if pmCount > 0 {
		s.AvgPM25 = round1(pmTotal / float64(pmCount))
	}
	trips, avgTrip := e.tripStats()
	s.Trips1h = int(float64(trips) * AgentScale)
	s.AvgTripMin = round1(avgTrip)
	var etaAll float64
	for _, cl := range e.calls {
		etaAll += cl.etaS
	}
	s.Calls1h = len(e.calls)
	s.AvgEtaMin = avgMin(etaAll, len(e.calls))
	s.Incidents = len(e.incidents)
	return t
}

func avgMin(totalS float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return round1(totalS / float64(n) / 60)
}

// ---- Alerts ----

type alertBook struct {
	active map[string]*events.Alert
	streak map[string]int
}

func newAlertBook() *alertBook {
	return &alertBook{active: map[string]*events.Alert{}, streak: map[string]int{}}
}

func (b *alertBook) list() []events.Alert {
	out := make([]events.Alert, 0, len(b.active))
	for _, a := range b.active {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.Before(out[j].OpenedAt)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// rule opens an alert when open is true, keeps it while keep is true and
// resolves it otherwise (hysteresis avoids flapping).
func (e *Engine) rule(key string, open, keep bool, build func() events.Alert) {
	a, active := e.alerts.active[key]
	switch {
	case !active && open:
		na := build()
		na.Key, na.Source = key, "rule"
		na.ID = fmt.Sprintf("%s@%d", key, e.Tick)
		na.OpenedAt = e.SimTime()
		e.alerts.active[key] = &na
		e.emit(events.Event{Kind: "alert.opened", Severity: na.Severity, EntityType: na.EntityType, EntityID: na.EntityID,
			Message: na.Title, Data: map[string]any{"alert_id": na.ID, "detail": na.Detail}})
	case active && !keep:
		now := e.SimTime()
		a.ResolvedAt = &now
		delete(e.alerts.active, key)
		e.emit(events.Event{Kind: "alert.resolved", Severity: "info", EntityType: a.EntityType, EntityID: a.EntityID,
			Message: "Resolved: " + a.Title, Data: map[string]any{"alert_id": a.ID}})
	case active:
		upd := build()
		a.Severity, a.Detail, a.Value, a.Title = upd.Severity, upd.Detail, upd.Value, upd.Title
	}
}

func (e *Engine) evaluateAlerts(t *events.Telemetry) {
	c := e.City
	for i, o := range t.Roads {
		road := c.Roads[i]
		if road.Class == city.Local {
			continue
		}
		key := "congestion:" + road.ID
		if o.Congestion >= 0.7 {
			e.alerts.streak[key]++
		} else {
			e.alerts.streak[key] = 0
		}
		o := o
		e.rule(key, e.alerts.streak[key] >= 3, o.Congestion >= 0.55, func() events.Alert {
			sev := "warning"
			if o.Congestion >= 0.85 {
				sev = "critical"
			}
			return events.Alert{Kind: "congestion", Severity: sev, EntityType: "road", EntityID: road.ID,
				Title:  fmt.Sprintf("Heavy congestion on %s", road.Name),
				Detail: fmt.Sprintf("%s: %.0f km/h vs %.0f km/h limit, congestion %.0f%%", road.Segment, o.SpeedKmh, road.SpeedKmh, o.Congestion*100),
				Value:  o.Congestion}
		})
	}
	for _, in := range e.incidents {
		road := c.Roads[in.road]
		in := in
		e.rule("incident:"+incidentID(in.id), true, true, func() events.Alert {
			return events.Alert{Kind: "incident", Severity: severityFor(in.severity), EntityType: "road", EntityID: road.ID,
				Title:  fmt.Sprintf("%s incident on %s", capitalize(in.severity), road.Name),
				Detail: fmt.Sprintf("%s, lanes reduced; expected to clear in %d min", road.Segment, max(0, (in.end-e.Tick)/60)),
				Value:  1}
		})
	}
	// Resolve incident alerts whose incident has cleared. Keys are sorted so
	// the resulting events come out in a deterministic order.
	var stale []string
	for key := range e.alerts.active {
		if strings.HasPrefix(key, "incident:") && !e.incidentActive(key[len("incident:"):]) {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		e.rule(key, false, false, nil)
	}
	for i, st := range t.Stations {
		f := c.Facilities[e.stations[i].facility]
		v := 0.0
		if st.PM25 != nil && st.Status == "ok" {
			v = *st.PM25
		}
		e.rule("aq:"+st.ID, v >= 55.5, v >= 45, func() events.Alert {
			sev := "warning"
			if v >= 150 {
				sev = "critical"
			}
			return events.Alert{Kind: "air_quality", Severity: sev, EntityType: "station", EntityID: st.ID,
				Title:  fmt.Sprintf("Unhealthy PM2.5 at %s", f.Name),
				Detail: fmt.Sprintf("PM2.5 %.1f µg/m³ (synthetic)", v), Value: v}
		})
		status := st.Status
		e.rule("sensor:"+st.ID, status != "ok", status != "ok", func() events.Alert {
			return events.Alert{Kind: "sensor_fault", Severity: "warning", EntityType: "station", EntityID: st.ID,
				Title: fmt.Sprintf("Sensor %s at %s", status, f.Name), Detail: "Readings from this station are unreliable", Value: 1}
		})
	}
	for i, d := range t.Districts {
		dist := c.Districts[i]
		d := d
		e.rule("grid:"+d.ID, d.Utilization >= 0.9, d.Utilization >= 0.85, func() events.Alert {
			sev := "warning"
			if d.Utilization >= 1 {
				sev = "critical"
			}
			return events.Alert{Kind: "grid_load", Severity: sev, EntityType: "district", EntityID: d.ID,
				Title:  fmt.Sprintf("Substation near capacity in %s", dist.Name),
				Detail: fmt.Sprintf("%.1f of %.0f MW (%.0f%%)", d.DemandMW, d.CapacityMW, d.Utilization*100), Value: d.Utilization}
		})
		e.rule("ems:"+d.ID, d.Calls1h >= 2 && d.AvgEtaMin > 10, d.Calls1h >= 1 && d.AvgEtaMin > 8, func() events.Alert {
			return events.Alert{Kind: "emergency_response", Severity: "warning", EntityType: "district", EntityID: d.ID,
				Title:  fmt.Sprintf("Slow emergency response in %s", dist.Name),
				Detail: fmt.Sprintf("Average ETA %.1f min over %d calls in the last hour", d.AvgEtaMin, d.Calls1h), Value: d.AvgEtaMin}
		})
	}
}

func (e *Engine) incidentActive(id string) bool {
	for _, in := range e.incidents {
		if incidentID(in.id) == id {
			return true
		}
	}
	return false
}
