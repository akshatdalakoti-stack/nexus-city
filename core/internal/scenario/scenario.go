// Package scenario runs what-if experiments. A scenario branches from a
// moment of a simulation run: because the simulation is deterministic, the
// exact city state at (seed, tick) is rebuilt by re-simulating from tick 0.
// The engine is then cloned into a baseline branch and a scenario branch;
// the intervention is applied to the scenario branch only, both are run for
// the same duration, and the results are compared.
//
// Outcomes are simulated. They show what this model of a fictional city
// does under a change, not evidence about any real city.
package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/sim"
)

// Kinds of scenario.
const (
	RoadClosure     = "road_closure"
	RushHour        = "rush_hour"
	SignalTiming    = "signal_timing"
	IndustrialSpike = "industrial_spike"
	Heatwave        = "heatwave"
)

var Kinds = []string{RoadClosure, RushHour, SignalTiming, IndustrialSpike, Heatwave}

// Limits keep scenarios cheap enough to run on a laptop.
const (
	MinDurationMin = 30
	MaxDurationMin = 360
	MaxBaseTick    = 7 * 86400 // branch from at most a week into a run
	SampleEvery    = 300       // seconds between comparison samples
	HistorySamples = 289       // 24 h of 5-minute telemetry kept for model forecasts
	MaxClosedRoads = 24        // enough to close a whole highway (Highway A has 12 segments)
)

// Params holds the settings for each kind; only the fields of the chosen kind are used.
type Params struct {
	RoadIDs        []string `json:"road_ids,omitempty"`        // road_closure
	DemandPct      float64  `json:"demand_pct,omitempty"`      // rush_hour: +% trips
	Policy         string   `json:"policy,omitempty"`          // signal_timing: adaptive
	DistrictID     string   `json:"district_id,omitempty"`     // industrial_spike
	EmissionFactor float64  `json:"emission_factor,omitempty"` // industrial_spike: x emissions
	TempDeltaC     float64  `json:"temp_delta_c,omitempty"`    // heatwave
}

// Spec fully determines a scenario: the same spec always gives the same result.
type Spec struct {
	Kind        string `json:"kind"`
	Seed        int64  `json:"seed"`
	BaseTick    int64  `json:"base_tick"`
	DurationMin int    `json:"duration_min"`
	Params      Params `json:"params"`
}

// Hash identifies a spec. It covers everything that influences the outcome.
func (s Spec) Hash() string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(append(b, []byte(city.Version)...))
	return hex.EncodeToString(sum[:8])
}

// Validate checks a spec against the city it will run on.
func (s Spec) Validate(c *city.City) error {
	if s.DurationMin < MinDurationMin || s.DurationMin > MaxDurationMin {
		return fmt.Errorf("duration_min must be between %d and %d", MinDurationMin, MaxDurationMin)
	}
	if s.BaseTick < 0 || s.BaseTick > MaxBaseTick {
		return fmt.Errorf("base_tick must be between 0 and %d", MaxBaseTick)
	}
	p := s.Params
	switch s.Kind {
	case RoadClosure:
		if len(p.RoadIDs) == 0 || len(p.RoadIDs) > MaxClosedRoads {
			return fmt.Errorf("road_closure needs 1–%d road_ids", MaxClosedRoads)
		}
		for _, id := range p.RoadIDs {
			if roadIndex(c, id) < 0 {
				return fmt.Errorf("unknown road %q", id)
			}
		}
	case RushHour:
		if p.DemandPct <= 0 || p.DemandPct > 200 {
			return fmt.Errorf("demand_pct must be in (0, 200]")
		}
	case SignalTiming:
		if p.Policy != sim.SignalAdaptive {
			return fmt.Errorf("policy must be %q", sim.SignalAdaptive)
		}
	case IndustrialSpike:
		d := districtIndex(c, p.DistrictID)
		if d < 0 {
			return fmt.Errorf("unknown district %q", p.DistrictID)
		}
		if c.Districts[d].Type != city.Industrial {
			return fmt.Errorf("district %s is not industrial", p.DistrictID)
		}
		if p.EmissionFactor <= 1 || p.EmissionFactor > 10 {
			return fmt.Errorf("emission_factor must be in (1, 10]")
		}
	case Heatwave:
		if p.TempDeltaC <= 0 || p.TempDeltaC > 15 {
			return fmt.Errorf("temp_delta_c must be in (0, 15]")
		}
	default:
		return fmt.Errorf("kind must be one of %s", strings.Join(Kinds, ", "))
	}
	return nil
}

// Intervention translates a validated spec into engine changes.
func (s Spec) Intervention(c *city.City) sim.Intervention {
	p := s.Params
	switch s.Kind {
	case RoadClosure:
		var roads []int
		for _, id := range p.RoadIDs {
			roads = append(roads, roadIndex(c, id))
		}
		return sim.Intervention{ClosedRoads: roads}
	case RushHour:
		return sim.Intervention{DemandFactor: 1 + p.DemandPct/100}
	case SignalTiming:
		return sim.Intervention{SignalPolicy: p.Policy}
	case IndustrialSpike:
		return sim.Intervention{Emission: map[int]float64{districtIndex(c, p.DistrictID): p.EmissionFactor}}
	case Heatwave:
		return sim.Intervention{TempDeltaC: p.TempDeltaC}
	}
	return sim.Intervention{}
}

func roadIndex(c *city.City, id string) int {
	return slices.IndexFunc(c.Roads, func(r city.Road) bool { return r.ID == id })
}

func districtIndex(c *city.City, id string) int {
	return slices.IndexFunc(c.Districts, func(d city.District) bool { return d.ID == id })
}

// ---------------------------------------------------------------- results

// Summary describes one branch over the scenario window.
type Summary struct {
	TripsCompleted  int     `json:"trips_completed"` // real vehicles (agents × scale)
	AvgTripMin      float64 `json:"avg_trip_min"`
	AvgCongestion   float64 `json:"avg_congestion"`
	PeakCongestion  float64 `json:"peak_congestion"`
	AvgSpeedKmh     float64 `json:"avg_speed_kmh"`
	AvgVehicles     float64 `json:"avg_vehicles"`
	EnergyMWh       float64 `json:"energy_mwh"`
	PeakDemandMW    float64 `json:"peak_demand_mw"`
	MaxUtilization  float64 `json:"max_utilization"`
	OverloadMinutes int     `json:"overload_minutes"` // district-minutes at or above 100% substation load
	AvgPM25         float64 `json:"avg_pm25"`         // mean of the modelled field
	MaxStationPM25  float64 `json:"max_station_pm25"`
	AvgEtaMin       float64 `json:"avg_eta_min"`
	AlertsOpened    int     `json:"alerts_opened"`
	Rerouted        int     `json:"rerouted"` // real vehicles
	Stranded        int     `json:"stranded"`
	AvgTempC        float64 `json:"avg_temp_c"`
}

// Point is one comparison sample (every 5 simulated minutes).
type Point struct {
	Tick       int64   `json:"tick"`
	Congestion float64 `json:"congestion"`
	SpeedKmh   float64 `json:"speed_kmh"`
	Vehicles   int     `json:"vehicles"`
	DemandMW   float64 `json:"demand_mw"`
	PM25       float64 `json:"pm25"`
	TripMin1h  float64 `json:"trip_min_1h"`
}

// RoadDelta is a road's mean congestion over the window in each branch.
type RoadDelta struct {
	ID       string  `json:"id"`
	Baseline float64 `json:"baseline"`
	Scenario float64 `json:"scenario"`
	Delta    float64 `json:"delta"`
	Closed   bool    `json:"closed,omitempty"`
}

// DistrictDelta compares district means over the window.
type DistrictDelta struct {
	ID               string  `json:"id"`
	DemandBaseline   float64 `json:"demand_baseline_mw"`
	DemandScenario   float64 `json:"demand_scenario_mw"`
	PeakUtilBaseline float64 `json:"peak_util_baseline"`
	PeakUtilScenario float64 `json:"peak_util_scenario"`
	PM25Baseline     float64 `json:"pm25_baseline"`
	PM25Scenario     float64 `json:"pm25_scenario"`
}

// Result is the full comparison. Forecasts are filled in by the worker
// from the model services.
type Result struct {
	SpecHash     string          `json:"spec_hash"`
	ResultHash   string          `json:"result_hash"`
	StartTime    time.Time       `json:"start_time"`
	EndTime      time.Time       `json:"end_time"`
	Baseline     Summary         `json:"baseline"`
	Scenario     Summary         `json:"scenario"`
	Series       SeriesPair      `json:"series"`
	Roads        []RoadDelta     `json:"roads"`
	Districts    []DistrictDelta `json:"districts"`
	PMGridDelta  []float64       `json:"pm_grid_delta"`
	SignalGreen  []int           `json:"signal_green_ns,omitempty"` // final adaptive plans (signal_timing)
	Forecasts    json.RawMessage `json:"forecasts,omitempty"`       // model forecasts per branch, added by the worker
	Context      json.RawMessage `json:"context,omitempty"`         // spatial context (PostGIS), added by the worker
	FastForwardS float64         `json:"fast_forward_s"`
	RunS         float64         `json:"run_s"`
	Note         string          `json:"note"`
}

type SeriesPair struct {
	Baseline []Point `json:"baseline"`
	Scenario []Point `json:"scenario"`
}

// Branches are the end-of-window telemetry histories (5-minute samples,
// oldest first) the worker sends to the forecast models.
type Branches struct {
	Baseline []*events.Telemetry
	Scenario []*events.Telemetry
}

// branch accumulates a summary for one engine.
type branch struct {
	e                *sim.Engine
	start            sim.Totals
	n                int
	cong, speed, veh float64
	peakCong         float64
	energy, peakMW   float64
	maxUtil          float64
	overload         int
	pm, maxPM        float64
	eta              float64
	etaN             int
	alerts           int
	temp             float64
	roadCong         []float64
	distDemand       []float64
	distPM           []float64
	distPeak         []float64
	pmGrid           []float64
	series           []Point
	history          []*events.Telemetry
}

func newBranch(e *sim.Engine, history []*events.Telemetry) *branch {
	c := e.City
	return &branch{e: e, start: e.Stats(), roadCong: make([]float64, len(c.Roads)), distDemand: make([]float64, len(c.Districts)),
		distPM: make([]float64, len(c.Districts)), distPeak: make([]float64, len(c.Districts)), pmGrid: make([]float64, c.Grid.Cols*c.Grid.Rows),
		history: slices.Clone(history)}
}

func (b *branch) observe(t *events.Telemetry) {
	s := t.Summary
	b.n++
	b.cong += s.AvgCongestion
	b.peakCong = math.Max(b.peakCong, s.AvgCongestion)
	b.speed += s.AvgSpeedKmh
	b.veh += float64(s.Vehicles)
	b.energy += s.DemandMW / 60
	b.peakMW = math.Max(b.peakMW, s.DemandMW)
	b.maxPM = math.Max(b.maxPM, s.MaxPM25)
	b.temp += t.Weather.TempC
	if s.Calls1h > 0 {
		b.eta += s.AvgEtaMin
		b.etaN++
	}
	var pm float64
	for i, v := range t.PMGrid {
		b.pmGrid[i] += v
		pm += v
	}
	b.pm += pm / float64(len(t.PMGrid))
	for i, o := range t.Roads {
		b.roadCong[i] += o.Congestion
	}
	for i, d := range t.Districts {
		b.distDemand[i] += d.DemandMW
		b.distPM[i] += d.PM25
		b.distPeak[i] = math.Max(b.distPeak[i], d.Utilization)
		b.maxUtil = math.Max(b.maxUtil, d.Utilization)
		if d.Utilization >= 1 {
			b.overload++
		}
	}
	if t.Tick%SampleEvery == 0 {
		b.series = append(b.series, Point{Tick: t.Tick, Congestion: s.AvgCongestion, SpeedKmh: s.AvgSpeedKmh, Vehicles: s.Vehicles,
			DemandMW: s.DemandMW, PM25: round(pm/float64(len(t.PMGrid)), 2), TripMin1h: s.AvgTripMin})
		b.history = append(b.history, t)
		if over := len(b.history) - HistorySamples; over > 0 {
			b.history = b.history[over:]
		}
	}
}

func (b *branch) countAlerts(evs []events.Event) {
	for _, ev := range evs {
		if ev.Kind == "alert.opened" {
			b.alerts++
		}
	}
}

func (b *branch) summary() Summary {
	n := float64(max(b.n, 1))
	end := b.e.Stats()
	trips := end.Trips - b.start.Trips
	s := Summary{
		TripsCompleted:  int(float64(trips) * sim.AgentScale),
		AvgCongestion:   round(b.cong/n, 4),
		PeakCongestion:  round(b.peakCong, 4),
		AvgSpeedKmh:     round(b.speed/n, 2),
		AvgVehicles:     round(b.veh/n, 0),
		EnergyMWh:       round(b.energy, 1),
		PeakDemandMW:    round(b.peakMW, 1),
		MaxUtilization:  round(b.maxUtil, 4),
		OverloadMinutes: b.overload,
		AvgPM25:         round(b.pm/n, 2),
		MaxStationPM25:  round(b.maxPM, 1),
		AlertsOpened:    b.alerts,
		Rerouted:        int(float64(end.Rerouted-b.start.Rerouted) * sim.AgentScale),
		Stranded:        int(float64(end.Stranded-b.start.Stranded) * sim.AgentScale),
		AvgTempC:        round(b.temp/n, 2),
	}
	if trips > 0 {
		s.AvgTripMin = round(float64(end.TripSecs-b.start.TripSecs)/float64(trips)/60, 2)
	}
	if b.etaN > 0 {
		s.AvgEtaMin = round(b.eta/float64(b.etaN), 2)
	}
	return s
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}

// Progress reports how far a run has got, from 0 to 1.
type Progress func(phase string, fraction float64)

// Run executes a validated spec. It is deterministic: the same spec gives
// the same ResultHash.
func Run(spec Spec, progress Progress) (*Result, *Branches, error) {
	if progress == nil {
		progress = func(string, float64) {}
	}
	c := city.Generate(spec.Seed)
	if err := spec.Validate(c); err != nil {
		return nil, nil, err
	}
	t0 := time.Now()
	e := sim.New(c)
	var history []*events.Telemetry
	keep := func(ts []*events.Telemetry) {
		for _, t := range ts {
			if t.Tick%SampleEvery == 0 {
				history = append(history, t)
			}
		}
		if over := len(history) - HistorySamples; over > 0 {
			history = history[over:]
		}
	}
	keep(e.DrainTelemetry())
	for e.Tick < spec.BaseTick {
		chunk := min(spec.BaseTick-e.Tick, 3600)
		e.StepN(int(chunk))
		keep(e.DrainTelemetry())
		e.DrainEvents()
		progress("rebuilding baseline", 0.5*float64(e.Tick)/float64(max(spec.BaseTick, 1)))
	}
	ff := time.Since(t0)

	t1 := time.Now()
	base := newBranch(e, history)
	scenEngine := e.Clone()
	if err := scenEngine.Apply(spec.Intervention(c)); err != nil {
		return nil, nil, err
	}
	scen := newBranch(scenEngine, history)
	steps := spec.DurationMin * 60
	for done := 0; done < steps; {
		chunk := min(steps-done, 600)
		for _, b := range []*branch{base, scen} {
			b.e.StepN(chunk)
			for _, t := range b.e.DrainTelemetry() {
				b.observe(t)
			}
			b.countAlerts(b.e.DrainEvents())
		}
		done += chunk
		progress("running branches", 0.5+0.5*float64(done)/float64(steps))
	}

	res := &Result{
		SpecHash:  spec.Hash(),
		StartTime: sim.Epoch.Add(time.Duration(spec.BaseTick) * time.Second),
		EndTime:   sim.Epoch.Add(time.Duration(spec.BaseTick+int64(steps)) * time.Second),
		Baseline:  base.summary(),
		Scenario:  scen.summary(),
		Series:    SeriesPair{Baseline: base.series, Scenario: scen.series},
		Note:      "Simulated outcome from a model of a fictional city; not real-world evidence.",
	}
	closed := map[int]bool{}
	for _, r := range spec.Intervention(c).ClosedRoads {
		closed[r] = true
	}
	n := float64(max(base.n, 1))
	for i, r := range c.Roads {
		bv, sv := base.roadCong[i]/n, scen.roadCong[i]/n
		res.Roads = append(res.Roads, RoadDelta{ID: r.ID, Baseline: round(bv, 4), Scenario: round(sv, 4), Delta: round(sv-bv, 4), Closed: closed[i]})
	}
	for i, d := range c.Districts {
		res.Districts = append(res.Districts, DistrictDelta{ID: d.ID,
			DemandBaseline: round(base.distDemand[i]/n, 2), DemandScenario: round(scen.distDemand[i]/n, 2),
			PeakUtilBaseline: round(base.distPeak[i], 4), PeakUtilScenario: round(scen.distPeak[i], 4),
			PM25Baseline: round(base.distPM[i]/n, 2), PM25Scenario: round(scen.distPM[i]/n, 2)})
	}
	res.PMGridDelta = make([]float64, len(base.pmGrid))
	for i := range base.pmGrid {
		res.PMGridDelta[i] = round((scen.pmGrid[i]-base.pmGrid[i])/n, 2)
	}
	if spec.Kind == SignalTiming {
		res.SignalGreen = scenEngine.GreenNS()
	}
	res.ResultHash = res.hash()
	res.FastForwardS = round(ff.Seconds(), 2)
	res.RunS = round(time.Since(t1).Seconds(), 2)
	return res, &Branches{Baseline: base.history, Scenario: scen.history}, nil
}

// hash fingerprints the simulated outcome (not timings or forecasts).
func (r *Result) hash() string {
	b, _ := json.Marshal(struct {
		B, S      Summary
		Series    SeriesPair
		Roads     []RoadDelta
		Districts []DistrictDelta
		Grid      []float64
	}{r.Baseline, r.Scenario, r.Series, r.Roads, r.Districts, r.PMGridDelta})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
