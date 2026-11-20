package sim

import (
	"math"
	"math/rand/v2"
	"time"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

type vehicle struct {
	id         int64
	route      []city.Link
	leg        int
	pos        float64 // metres along the current road, in travel direction
	speed      float64 // m/s
	waiting    bool    // stopped at a red light at the end of the road
	originDist int
	destDist   int
	depart     int64
}

type roadState struct {
	count      [2]int     // agents per direction
	density    [2]float64 // real vehicles per km per lane
	speed      [2]float64 // m/s
	laneFactor float64    // < 1 while an incident blocks lanes
	incident   int        // active incident id, 0 if none
}

type minuteAcc struct {
	steps    int
	flow     []float64
	cong     []float64
	speed    []float64
	queue    []float64
	queueNS  []float64 // waiting on north-south approaches (for adaptive signals)
	trips    int
	tripSecs int64
}

func (a *minuteAcc) reset(roads, nodes int) {
	a.steps = 0
	a.flow = make([]float64, roads)
	a.cong = make([]float64, roads)
	a.speed = make([]float64, roads)
	a.queue = make([]float64, nodes)
	a.queueNS = make([]float64, nodes)
	a.trips = 0
	a.tripSecs = 0
}

func bump(h, mu, sd float64) float64 {
	z := (h - mu) / sd
	return math.Exp(-0.5 * z * z)
}

func hourOf(t time.Time) float64 {
	return float64(t.Hour()) + float64(t.Minute())/60 + float64(t.Second())/3600
}

func isWeekend(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// TripRatePerMinute is the city-wide number of agent trips starting per
// simulated minute: a double commuter peak on weekdays, a single midday
// hump at weekends.
func TripRatePerMinute(t time.Time) float64 {
	h := hourOf(t)
	if isWeekend(t) {
		return 6 + 42*bump(h, 13, 3.2) + 18*bump(h, 19.5, 2)
	}
	return 5 + 26*bump(h, 13, 3.5) + 64*bump(h, 8.1, 1.1) + 70*bump(h, 17.8, 1.4)
}

// poisson draws from a Poisson distribution (Knuth's method; fine for the
// small rates used here).
func poisson(r *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	l := math.Exp(-lambda)
	k := 0
	p := 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func weightedPick(r *rand.Rand, w []float64) int {
	total := 0.0
	for _, x := range w {
		total += x
	}
	u := r.Float64() * total
	for i, x := range w {
		u -= x
		if u < 0 {
			return i
		}
	}
	return len(w) - 1
}

func (e *Engine) roadCost(road int) float64 {
	if e.iv.closed[road] {
		return math.Inf(1)
	}
	return e.travelS[road]
}

func (e *Engine) signalPenalty(node int) float64 {
	n := &e.City.Nodes[node]
	if !n.Signalized {
		return 0
	}
	return float64(n.CycleS) / 6 // expected wait at a two-phase signal
}

func (e *Engine) spawnTrips() {
	lambda := TripRatePerMinute(e.SimTime()) / 60 * e.iv.demandFactor
	n := poisson(e.rTraffic, lambda)
	for i := 0; i < n; i++ {
		if len(e.vehicles) >= MaxAgents {
			return
		}
		e.spawnTrip()
	}
}

func (e *Engine) spawnTrip() {
	t := e.SimTime()
	h := hourOf(t)
	ow, dw := e.actW, e.actW
	mode := e.rTraffic.Float64()
	weekday := !isWeekend(t)
	switch {
	case weekday && h >= 5 && h < 11 && mode < 0.7:
		ow, dw = e.popW, e.jobW // morning commute: homes → jobs
	case weekday && h >= 15 && h < 21 && mode < 0.7:
		ow, dw = e.jobW, e.popW // evening commute: jobs → homes
	}
	od := weightedPick(e.rTraffic, ow)
	dd := weightedPick(e.rTraffic, dw)
	if dd == od && e.rTraffic.Float64() < 0.85 {
		dd = weightedPick(e.rTraffic, dw)
	}
	on := e.pickNode(e.rTraffic, od)
	dn := e.pickNode(e.rTraffic, dd)
	if on == dn {
		return
	}
	route, _, ok := e.City.ShortestPath(on, dn, e.roadCost, e.signalPenalty)
	if !ok || len(route) == 0 {
		return
	}
	v := &vehicle{id: e.nextVehID, route: route, originDist: od, destDist: dd, depart: e.Tick}
	e.nextVehID++
	first := route[0]
	e.roads[first.Road].count[first.Dir]++
	e.vehicles = append(e.vehicles, v)
}

func (e *Engine) pickNode(r *rand.Rand, district int) int {
	nodes := e.City.Districts[district].Nodes
	return nodes[r.IntN(len(nodes))]
}

// updateRoadSpeeds applies the Greenshields speed–density relation to each
// road direction: speed falls linearly from free-flow to zero at jam density.
func (e *Engine) updateRoadSpeeds() {
	for i := range e.roads {
		rs := &e.roads[i]
		road := &e.City.Roads[i]
		lanes := float64(road.Lanes) * rs.laneFactor
		lenKm := road.LengthM / 1000
		vfree := road.SpeedKmh / 3.6
		for d := 0; d < 2; d++ {
			k := float64(rs.count[d]) * AgentScale / (lenKm * lanes)
			rs.density[d] = k
			v := vfree * (1 - k/JamDensity)
			if rs.incident != 0 {
				v = math.Min(v, vfree*0.45) // drivers slow past the scene
			}
			rs.speed[d] = math.Max(v, MinSpeedMS)
		}
	}
}

// accumulateRoads adds this step's road measurements to the minute averages,
// the way a loop detector would report them.
func (e *Engine) accumulateRoads() {
	a := &e.acc
	a.steps++
	for i := range e.roads {
		rs := &e.roads[i]
		road := &e.City.Roads[i]
		lanes := float64(road.Lanes) * rs.laneFactor
		vfree := road.SpeedKmh
		flow, cong, spdW := 0.0, 0.0, 0.0
		cnt := 0
		for d := 0; d < 2; d++ {
			vk := rs.speed[d] * 3.6
			flow += rs.density[d] * vk * lanes
			if rs.count[d] > 0 {
				cong = math.Max(cong, 1-vk/vfree)
				spdW += vk * float64(rs.count[d])
				cnt += rs.count[d]
			}
		}
		spd := vfree
		if cnt > 0 {
			spd = spdW / float64(cnt)
		}
		a.flow[i] += flow
		a.cong[i] += cong
		a.speed[i] += spd
	}
}

func (e *Engine) isRed(node int, axis string, tick int64) bool {
	n := &e.City.Nodes[node]
	if !n.Signalized {
		return false
	}
	phase := (tick + int64(n.OffsetS)) % int64(n.CycleS)
	nsGreen := phase < int64(e.iv.greenNS[node])
	if axis == "ns" {
		return !nsGreen
	}
	return nsGreen
}

func (e *Engine) moveVehicles() {
	kept := e.vehicles[:0]
	for _, v := range e.vehicles {
		if e.advance(v) {
			kept = append(kept, v)
			if v.waiting {
				l := v.route[v.leg]
				e.acc.queue[l.To] += AgentScale
				if e.City.Roads[l.Road].Axis == "ns" {
					e.acc.queueNS[l.To] += AgentScale
				}
			}
		}
	}
	for i := len(kept); i < len(e.vehicles); i++ {
		e.vehicles[i] = nil
	}
	e.vehicles = kept
}

// advance moves one vehicle for one step. It returns false when the vehicle
// has reached its destination and leaves the network.
func (e *Engine) advance(v *vehicle) bool {
	link := v.route[v.leg]
	road := &e.City.Roads[link.Road]
	rs := &e.roads[link.Road]
	if v.waiting {
		if e.isRed(link.To, road.Axis, e.Tick) {
			v.speed = 0
			return true
		}
		v.waiting = false
		e.nextLeg(v, 0)
		return true
	}
	v.speed = rs.speed[link.Dir]
	v.pos += v.speed * StepSeconds
	if v.pos < road.LengthM {
		return true
	}
	if v.leg == len(v.route)-1 {
		rs.count[link.Dir]--
		e.acc.trips++
		e.acc.tripSecs += e.Tick + 1 - v.depart
		e.stats.Trips++
		e.stats.TripSecs += e.Tick + 1 - v.depart
		return false
	}
	if e.iv.anyClosed && !e.rerouteIfBlocked(v) {
		rs.count[link.Dir]--
		e.stats.Stranded++
		return false
	}
	if e.isRed(link.To, road.Axis, e.Tick) {
		v.pos = road.LengthM
		v.speed = 0
		v.waiting = true
		return true
	}
	e.nextLeg(v, v.pos-road.LengthM)
	return true
}

func (e *Engine) nextLeg(v *vehicle, overflow float64) {
	cur := v.route[v.leg]
	e.roads[cur.Road].count[cur.Dir]--
	v.leg++
	nxt := v.route[v.leg]
	e.roads[nxt.Road].count[nxt.Dir]++
	v.pos = math.Min(overflow, e.City.Roads[nxt.Road].LengthM/2)
}

// closeRoadMinute turns the accumulated per-step measurements into minute
// averages and refreshes the routing costs.
func (e *Engine) closeRoadMinute() []events.RoadObs {
	a := &e.acc
	steps := float64(max(a.steps, 1))
	obs := make([]events.RoadObs, len(e.City.Roads))
	for i, road := range e.City.Roads {
		rs := &e.roads[i]
		spd := road.SpeedKmh
		if a.steps > 0 {
			spd = a.speed[i] / steps
		}
		o := events.RoadObs{
			ID:         road.ID,
			FlowVph:    round1(a.flow[i] / steps),
			SpeedKmh:   round1(spd),
			Congestion: round3(a.cong[i] / steps),
			Vehicles:   int(float64(rs.count[0]+rs.count[1]) * AgentScale),
		}
		if rs.incident != 0 {
			o.Incident = incidentID(rs.incident)
		}
		obs[i] = o
		e.travelS[i] = road.LengthM / (math.Max(spd, 3) / 3.6)
	}
	e.lastRoadObs = obs
	return obs
}

type tripMinute struct {
	trips    int
	tripSecs int64
}

func (e *Engine) rollTrips() {
	slot := (e.Tick / 60) % 60
	e.tripsRing[slot] = tripMinute{trips: e.acc.trips, tripSecs: e.acc.tripSecs}
}

func (e *Engine) tripStats() (int, float64) {
	trips := 0
	var secs int64
	for _, m := range e.tripsRing {
		trips += m.trips
		secs += m.tripSecs
	}
	avg := 0.0
	if trips > 0 {
		avg = float64(secs) / float64(trips) / 60
	}
	return trips, avg
}

// Frame returns the current vehicle positions. Vehicles are drawn 5 m to the
// right of the road centre line so the two directions do not overlap.
func (e *Engine) Frame() events.Frame {
	f := events.Frame{SimTime: e.SimTime(), Tick: e.Tick, Scale: AgentScale,
		Vehicles: make([][5]float64, 0, len(e.vehicles))}
	for _, v := range e.vehicles {
		l := v.route[v.leg]
		road := &e.City.Roads[l.Road]
		a, b := e.City.Nodes[road.A], e.City.Nodes[road.B]
		if l.Dir == 1 {
			a, b = b, a
		}
		dx, dy := b.X-a.X, b.Y-a.Y
		length := math.Hypot(dx, dy)
		frac := math.Min(v.pos/road.LengthM, 1)
		x := a.X + dx*frac + dy/length*5
		y := a.Y + dy*frac - dx/length*5
		lon, lat := e.City.Origin.LonLat(x, y)
		heading := math.Mod(math.Atan2(dx, dy)*180/math.Pi+360, 360)
		f.Vehicles = append(f.Vehicles, [5]float64{float64(v.id), lon, lat, math.Round(heading), round1(v.speed * 3.6)})
	}
	return f
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
