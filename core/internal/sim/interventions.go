package sim

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

// Signal policies.
const (
	SignalFixed    = "fixed"    // the city's fixed-time plans (normal operation)
	SignalAdaptive = "adaptive" // green split follows queues, re-planned every minute
)

// Intervention is a what-if change applied to a running engine. The zero
// value of each field means "no change".
type Intervention struct {
	ClosedRoads  []int           // road indices closed to new entries
	DemandFactor float64         // multiplier on trip generation (e.g. 1.3 = +30%); 0 means unchanged
	SignalPolicy string          // SignalFixed or SignalAdaptive; "" means unchanged
	Emission     map[int]float64 // district index -> industrial emission multiplier
	TempDeltaC   float64         // added to the simulated temperature
}

type interventionState struct {
	closed       []bool
	anyClosed    bool
	demandFactor float64
	adaptive     bool
	greenNS      []int     // current north-south green seconds per node
	emission     []float64 // per district
	tempDelta    float64
}

func neutralInterventions(c *city.City) interventionState {
	s := interventionState{
		closed:       make([]bool, len(c.Roads)),
		demandFactor: 1,
		greenNS:      make([]int, len(c.Nodes)),
		emission:     make([]float64, len(c.Districts)),
	}
	for i, n := range c.Nodes {
		s.greenNS[i] = n.GreenNSS
	}
	for i := range s.emission {
		s.emission[i] = 1
	}
	return s
}

// Apply changes the engine from now on. It is deterministic and draws no
// random numbers, so a branch differs from its baseline only through the
// intervention itself.
func (e *Engine) Apply(iv Intervention) error {
	for _, r := range iv.ClosedRoads {
		if r < 0 || r >= len(e.City.Roads) {
			return fmt.Errorf("road index %d out of range", r)
		}
		e.iv.closed[r] = true
		e.iv.anyClosed = true
	}
	if iv.DemandFactor > 0 {
		e.iv.demandFactor = iv.DemandFactor
	}
	switch iv.SignalPolicy {
	case "":
	case SignalFixed:
		e.iv.adaptive = false
	case SignalAdaptive:
		e.iv.adaptive = true
	default:
		return fmt.Errorf("unknown signal policy %q", iv.SignalPolicy)
	}
	for d, f := range iv.Emission {
		if d < 0 || d >= len(e.iv.emission) || f < 0 {
			return fmt.Errorf("invalid emission factor for district %d", d)
		}
		e.iv.emission[d] = f
	}
	e.iv.tempDelta += iv.TempDeltaC
	return nil
}

// Stats returns cumulative counters since tick 0.
func (e *Engine) Stats() Totals { return e.stats }

// rerouteIfBlocked is called when a vehicle reaches the end of a road and
// more of its route remains. If a remaining road is closed, it re-plans from
// here. It returns false if no route to the destination exists any more.
func (e *Engine) rerouteIfBlocked(v *vehicle) bool {
	rest := v.route[v.leg+1:]
	blocked := slices.ContainsFunc(rest, func(l city.Link) bool { return e.iv.closed[l.Road] })
	if !blocked {
		return true
	}
	here := v.route[v.leg].To
	dest := v.route[len(v.route)-1].To
	path, _, ok := e.City.ShortestPath(here, dest, e.roadCost, e.signalPenalty)
	if !ok || len(path) == 0 {
		return false
	}
	route := make([]city.Link, 0, v.leg+1+len(path))
	route = append(route, v.route[:v.leg+1]...)
	v.route = append(route, path...)
	e.stats.Rerouted++
	return true
}

// adaptSignals re-plans every signal's green split from the last minute's
// queues: each axis gets green time in proportion to its waiting vehicles,
// kept between 25% and 75% of the cycle and smoothed so plans change
// gradually. Called once per simulated minute when the policy is adaptive.
func (e *Engine) adaptSignals() {
	for i, n := range e.City.Nodes {
		if !n.Signalized {
			continue
		}
		ns := e.acc.queueNS[i]
		ew := e.acc.queue[i] - ns
		share := (ns + 1) / (ns + ew + 2)
		target := float64(n.CycleS) * math.Max(0.25, math.Min(0.75, share))
		g := 0.5*float64(e.iv.greenNS[i]) + 0.5*target
		e.iv.greenNS[i] = int(math.Round(g))
	}
}

// GreenNS returns the current north-south green time of every intersection.
func (e *Engine) GreenNS() []int { return slices.Clone(e.iv.greenNS) }

// bindRandom (re)creates the random generators from the stored sources.
func (e *Engine) bindRandom() {
	r := func(i uint64) *rand.Rand { return rand.New(e.sources[i-1]) }
	e.rTraffic, e.rIncident, e.rEnv = r(streamTraffic), r(streamIncident), r(streamEnv)
	e.rEnergy, e.rFault, e.rEmergency, e.rAnomaly = r(streamEnergy), r(streamFault), r(streamEmergency), r(streamAnomaly)
}

// Clone returns an independent deep copy. Stepping the copy and the
// original produces identical states until one of them is changed; this is
// how a scenario branches from its baseline.
func (e *Engine) Clone() *Engine {
	c := *e // copies scalars, arrays and the city pointer (the city is immutable)
	for i, s := range e.sources {
		cp := *s
		c.sources[i] = &cp
	}
	c.bindRandom()
	c.vehicles = make([]*vehicle, len(e.vehicles))
	for i, v := range e.vehicles {
		cv := *v // routes are never modified in place, so sharing them is safe
		c.vehicles[i] = &cv
	}
	c.roads = slices.Clone(e.roads)
	c.travelS = slices.Clone(e.travelS)
	c.pm = slices.Clone(e.pm)
	c.stations = slices.Clone(e.stations)
	c.energy = slices.Clone(e.energy)
	c.incidents = make([]*incident, len(e.incidents))
	for i, in := range e.incidents {
		ci := *in
		c.incidents[i] = &ci
	}
	c.calls = slices.Clone(e.calls)
	c.surges = slices.Clone(e.surges)
	c.releases = slices.Clone(e.releases)
	c.alerts = &alertBook{active: make(map[string]*events.Alert, len(e.alerts.active)), streak: make(map[string]int, len(e.alerts.streak))}
	for k, a := range e.alerts.active {
		ca := *a
		c.alerts.active[k] = &ca
	}
	for k, v := range e.alerts.streak {
		c.alerts.streak[k] = v
	}
	c.acc = minuteAcc{
		steps: e.acc.steps, trips: e.acc.trips, tripSecs: e.acc.tripSecs,
		flow: slices.Clone(e.acc.flow), cong: slices.Clone(e.acc.cong), speed: slices.Clone(e.acc.speed),
		queue: slices.Clone(e.acc.queue), queueNS: slices.Clone(e.acc.queueNS),
	}
	c.outTelem, c.outEvents = nil, nil
	c.iv = interventionState{
		closed: slices.Clone(e.iv.closed), anyClosed: e.iv.anyClosed, demandFactor: e.iv.demandFactor,
		adaptive: e.iv.adaptive, greenNS: slices.Clone(e.iv.greenNS), emission: slices.Clone(e.iv.emission),
		tempDelta: e.iv.tempDelta,
	}
	return &c
}
