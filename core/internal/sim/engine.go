// Package sim is the deterministic city simulation. Its state after N steps
// is a pure function of (seed, N): wall-clock speed, pauses and how often
// results are read never change what happens. Each subsystem draws from its
// own random stream so adding randomness to one never perturbs another.
package sim

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"time"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

// Epoch is simulated time at tick 0: Monday 1 June 2026, 05:30 city time.
var Epoch = time.Date(2026, time.June, 1, 5, 30, 0, 0, time.UTC)

const (
	StepSeconds = 1.0   // fixed simulation step
	AgentScale  = 8.0   // real vehicles represented by one simulated agent
	JamDensity  = 130.0 // vehicles per km per lane at standstill
	MinSpeedMS  = 1.2   // crawl speed floor, ~4 km/h
	MaxAgents   = 2500
)

// Random stream identifiers, mixed with the seed.
const (
	streamTraffic uint64 = iota + 1
	streamIncident
	streamEnv
	streamEnergy
	streamFault
	streamEmergency
	streamAnomaly
)

// Engine holds the full mutable state of one simulation run.
type Engine struct {
	City *city.City
	Seed int64
	Tick int64

	vehicles  []*vehicle
	nextVehID int64
	roads     []roadState
	travelS   []float64 // minute-averaged travel time per road, used for routing

	rTraffic, rIncident, rEnv, rEnergy, rFault, rEmergency, rAnomaly *rand.Rand
	sources                                                          [7]*rand.PCG // kept so Clone can copy RNG state

	popW, jobW, actW []float64 // district weights for trip generation

	envPhase  [8]float64
	weather   events.Weather
	pm        []float64
	cellDist  []int // district of each pollution cell
	stacks    []stack
	stations  []stationState
	energy    []energyState
	incidents []*incident
	incSeq    int
	calls     []call
	callSeq   int
	hospitals []int // facility indices
	surges    []disturbance
	releases  []disturbance
	alerts    *alertBook
	tripsRing [60]tripMinute
	acc       minuteAcc

	latest      *events.Telemetry
	outTelem    []*events.Telemetry
	outEvents   []events.Event
	lastRoadObs []events.RoadObs

	iv    interventionState // what-if changes; all neutral in a normal run
	stats Totals
}

// Totals are cumulative counters since tick 0, used to measure a window.
type Totals struct {
	Trips    int64 // agent trips completed
	TripSecs int64 // their summed duration
	Rerouted int64 // agents that changed route because a road closed
	Stranded int64 // agents left with no route to their destination
}

// New creates an engine at tick 0 for the given city.
func New(c *city.City) *Engine {
	seed := uint64(c.Seed)
	e := &Engine{
		City:      c,
		Seed:      c.Seed,
		roads:     make([]roadState, len(c.Roads)),
		travelS:   make([]float64, len(c.Roads)),
		alerts:    newAlertBook(),
		hospitals: c.FacilitiesOf(city.Hospital),
	}
	for i := range e.sources {
		e.sources[i] = rand.NewPCG(seed, uint64(i+1))
	}
	e.bindRandom()
	e.iv = neutralInterventions(c)
	for i := range e.roads {
		e.roads[i].laneFactor = 1
		r := c.Roads[i]
		e.travelS[i] = r.LengthM / (r.SpeedKmh / 3.6)
	}
	for _, d := range c.Districts {
		e.popW = append(e.popW, float64(d.Population))
		e.jobW = append(e.jobW, float64(d.Jobs))
		e.actW = append(e.actW, float64(d.Population+d.Jobs))
	}
	e.acc.reset(len(c.Roads), len(c.Nodes))
	e.initEnvironment()
	e.initEnergy()
	e.initAnomalies()
	e.minuteUpdate() // produce the tick-0 observation
	return e
}

// SimTime is the current simulated time.
func (e *Engine) SimTime() time.Time {
	return Epoch.Add(time.Duration(e.Tick) * time.Second)
}

// Step advances the simulation by one fixed step.
func (e *Engine) Step() {
	e.spawnTrips()
	e.updateRoadSpeeds()
	e.accumulateRoads()
	e.moveVehicles()
	e.Tick++
	if e.Tick%60 == 0 {
		e.minuteUpdate()
	}
}

// StepN advances n steps.
func (e *Engine) StepN(n int) {
	for i := 0; i < n; i++ {
		e.Step()
	}
}

// Latest returns the most recent minute observation.
func (e *Engine) Latest() *events.Telemetry { return e.latest }

// DrainTelemetry returns observations produced since the last call.
func (e *Engine) DrainTelemetry() []*events.Telemetry {
	out := e.outTelem
	e.outTelem = nil
	return out
}

// DrainEvents returns events produced since the last call.
func (e *Engine) DrainEvents() []events.Event {
	out := e.outEvents
	e.outEvents = nil
	return out
}

// AgentCount is the number of simulated vehicles on the network.
func (e *Engine) AgentCount() int { return len(e.vehicles) }

func (e *Engine) emit(ev events.Event) {
	ev.SimTime = e.SimTime()
	e.outEvents = append(e.outEvents, ev)
}

// minuteUpdate runs every simulated minute: it closes the traffic averages,
// updates routing costs, incidents, weather, pollution, sensors, energy and
// emergency demand, evaluates alert rules and publishes an observation.
func (e *Engine) minuteUpdate() {
	roadObs := e.closeRoadMinute()
	if e.iv.adaptive {
		e.adaptSignals()
	}
	e.updateIncidents()
	e.updateWeather()
	e.updateAnomalies()
	e.updatePollution(roadObs)
	e.updateStations()
	e.updateEnergy()
	e.updateEmergency()
	e.rollTrips()
	t := e.buildTelemetry(roadObs)
	e.evaluateAlerts(t)
	t.Alerts = e.alerts.list()
	t.Summary.Alerts = len(t.Alerts)
	e.latest = t
	e.outTelem = append(e.outTelem, t)
	e.acc.reset(len(e.City.Roads), len(e.City.Nodes))
}

// StateHash fingerprints the full simulation state. Two engines with the
// same seed and tick must produce the same hash.
func (e *Engine) StateHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "tick=%d seed=%d next=%d\n", e.Tick, e.Seed, e.nextVehID)
	for _, v := range e.vehicles {
		l := v.route[v.leg]
		fmt.Fprintf(h, "v %d %d %d %v %v %t\n", v.id, v.leg, l.Road, v.pos, v.speed, v.waiting)
	}
	for _, p := range e.pm {
		fmt.Fprintf(h, "%v,", p)
	}
	for _, s := range e.energy {
		fmt.Fprintf(h, "e %v %v\n", s.demand, s.ar)
	}
	for _, s := range e.stations {
		fmt.Fprintf(h, "s %s %v %d\n", s.status, s.last, s.faultUntil)
	}
	for _, in := range e.incidents {
		fmt.Fprintf(h, "i %d %d %d\n", in.id, in.road, in.end)
	}
	for _, s := range e.surges {
		fmt.Fprintf(h, "su %v %d\n", s.factor, s.end)
	}
	for _, r := range e.releases {
		fmt.Fprintf(h, "re %v %d\n", r.factor, r.end)
	}
	fmt.Fprintf(h, "iv green=%v closed=%v demand=%v emission=%v temp=%v\n",
		e.iv.greenNS, e.iv.closed, e.iv.demandFactor, e.iv.emission, e.iv.tempDelta)
	fmt.Fprintf(h, "calls=%d alerts=%d\n", len(e.calls), len(e.alerts.active))
	return hex.EncodeToString(h.Sum(nil))
}
