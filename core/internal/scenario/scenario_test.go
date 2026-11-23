package scenario

import (
	"testing"

	"nexuscity/internal/city"
	"nexuscity/internal/sim"
)

const morningRush = 2*3600 + 1800 // 08:00 on the first weekday

func TestSameSpecSameResult(t *testing.T) {
	spec := Spec{Kind: RushHour, Seed: 42, BaseTick: 3600, DurationMin: 30, Params: Params{DemandPct: 40}}
	a, _, err := Run(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := Run(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ResultHash != b.ResultHash || a.SpecHash != b.SpecHash {
		t.Fatalf("not reproducible: %s vs %s", a.ResultHash, b.ResultHash)
	}
	spec.Params.DemandPct = 41
	c, _, _ := Run(spec, nil)
	if c.ResultHash == a.ResultHash || c.SpecHash == a.SpecHash {
		t.Fatal("a different spec produced the same result")
	}
}

// The baseline branch must equal the live simulation: branching must not
// change the baseline itself.
func TestBaselineBranchMatchesPlainRun(t *testing.T) {
	spec := Spec{Kind: Heatwave, Seed: 42, BaseTick: 1800, DurationMin: 30, Params: Params{TempDeltaC: 5}}
	res, branches, err := Run(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	plain := sim.New(city.Generate(42))
	plain.StepN(1800 + 30*60)
	want := plain.Latest().Summary
	got := branches.Baseline[len(branches.Baseline)-1].Summary
	if got != want {
		t.Fatalf("baseline branch diverged from a plain run:\n got %+v\nwant %+v", got, want)
	}
	if res.Baseline.AvgTempC >= res.Scenario.AvgTempC {
		t.Fatal("heatwave branch is not hotter")
	}
}

func TestEachKindMovesItsMetric(t *testing.T) {
	c := city.Generate(42)
	// The busiest major road at 08:00, found by simulating to that moment.
	e := sim.New(c)
	e.StepN(morningRush)
	busiest, best := "", -1.0
	for i, o := range e.Latest().Roads {
		if c.Roads[i].Class != city.Local && o.FlowVph > best {
			busiest, best = o.ID, o.FlowVph
		}
	}
	cases := []struct {
		spec  Spec
		check func(r *Result) (bool, string)
	}{
		{Spec{Kind: RoadClosure, Params: Params{RoadIDs: []string{busiest}}}, func(r *Result) (bool, string) {
			return r.Scenario.Rerouted > 0 && r.Scenario.AvgTripMin > r.Baseline.AvgTripMin, "closure should reroute and lengthen trips"
		}},
		{Spec{Kind: RushHour, Params: Params{DemandPct: 50}}, func(r *Result) (bool, string) {
			return r.Scenario.AvgVehicles > r.Baseline.AvgVehicles && r.Scenario.AvgCongestion > r.Baseline.AvgCongestion, "more trips should add vehicles and congestion"
		}},
		{Spec{Kind: IndustrialSpike, Params: Params{DistrictID: "D1", EmissionFactor: 4}}, func(r *Result) (bool, string) {
			return r.Districts[0].PM25Scenario > r.Districts[0].PM25Baseline, "emissions should raise PM2.5 in the district"
		}},
		{Spec{Kind: Heatwave, Params: Params{TempDeltaC: 8}}, func(r *Result) (bool, string) {
			return r.Scenario.EnergyMWh > r.Baseline.EnergyMWh, "heat should raise electricity demand"
		}},
		{Spec{Kind: SignalTiming, Params: Params{Policy: sim.SignalAdaptive}}, func(r *Result) (bool, string) {
			return len(r.SignalGreen) == len(c.Nodes) && r.ResultHash != "", "adaptive signals should report their plans"
		}},
	}
	for _, tc := range cases {
		tc.spec.Seed, tc.spec.BaseTick, tc.spec.DurationMin = 42, morningRush, 60
		r, _, err := Run(tc.spec, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.spec.Kind, err)
		}
		if ok, why := tc.check(r); !ok {
			t.Errorf("%s: %s (baseline %+v, scenario %+v)", tc.spec.Kind, why, r.Baseline, r.Scenario)
		}
	}
}

func TestValidate(t *testing.T) {
	c := city.Generate(42)
	ok := Spec{Kind: Heatwave, Seed: 42, BaseTick: 0, DurationMin: 60, Params: Params{TempDeltaC: 5}}
	if err := ok.Validate(c); err != nil {
		t.Fatal(err)
	}
	bad := []Spec{
		{Kind: "earthquake", DurationMin: 60},
		{Kind: Heatwave, DurationMin: 5, Params: Params{TempDeltaC: 5}},
		{Kind: Heatwave, DurationMin: 60, BaseTick: MaxBaseTick + 1, Params: Params{TempDeltaC: 5}},
		{Kind: Heatwave, DurationMin: 60, Params: Params{TempDeltaC: 40}},
		{Kind: RoadClosure, DurationMin: 60, Params: Params{RoadIDs: []string{"R9999"}}},
		{Kind: RoadClosure, DurationMin: 60},
		{Kind: RushHour, DurationMin: 60, Params: Params{DemandPct: -10}},
		{Kind: SignalTiming, DurationMin: 60, Params: Params{Policy: "random"}},
		{Kind: IndustrialSpike, DurationMin: 60, Params: Params{DistrictID: "D6", EmissionFactor: 3}}, // commercial
	}
	for _, s := range bad {
		if s.Validate(c) == nil {
			t.Errorf("accepted invalid spec %+v", s)
		}
	}
}
