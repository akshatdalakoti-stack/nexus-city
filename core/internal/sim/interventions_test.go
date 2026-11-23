package sim

import (
	"testing"

	"nexuscity/internal/city"
)

func TestCloneStepsIdentically(t *testing.T) {
	e := run(42, 2*3600)
	c := e.Clone()
	if c.StateHash() != e.StateHash() {
		t.Fatal("clone differs immediately")
	}
	e.StepN(3600)
	c.StepN(3600)
	if c.StateHash() != e.StateHash() {
		t.Fatal("clone diverged from the original")
	}
}

func TestCloneIsIndependent(t *testing.T) {
	e := run(42, 2*3600)
	before := e.StateHash()
	c := e.Clone()
	if err := c.Apply(Intervention{DemandFactor: 2}); err != nil {
		t.Fatal(err)
	}
	c.StepN(1800)
	if e.StateHash() != before {
		t.Fatal("stepping the clone changed the original")
	}
}

func TestNeutralInterventionChangesNothing(t *testing.T) {
	a, b := run(42, 3600), run(42, 3600)
	if err := b.Apply(Intervention{}); err != nil {
		t.Fatal(err)
	}
	a.StepN(3600)
	b.StepN(3600)
	if a.StateHash() != b.StateHash() {
		t.Fatal("an empty intervention changed the simulation")
	}
}

func busiestRoad(e *Engine) int {
	best, n := 0, -1
	for i, rs := range e.roads {
		if c := rs.count[0] + rs.count[1]; c > n && e.City.Roads[i].Class != city.Local {
			best, n = i, c
		}
	}
	return best
}

func TestRoadClosureReroutesTraffic(t *testing.T) {
	e := run(42, 2*3600+1800) // 08:00 on a weekday
	closed := busiestRoad(e)
	if err := e.Apply(Intervention{ClosedRoads: []int{closed}}); err != nil {
		t.Fatal(err)
	}
	// Vehicles already on the road may finish it; after that nobody enters.
	e.StepN(15 * 60)
	if n := e.roads[closed].count[0] + e.roads[closed].count[1]; n != 0 {
		t.Fatalf("%d agents still on the closed road after 15 minutes", n)
	}
	for _, v := range e.vehicles {
		for _, l := range v.route[v.leg:] {
			if l.Road == closed {
				t.Fatalf("vehicle %d still plans to use the closed road", v.id)
			}
		}
	}
	if e.Stats().Rerouted == 0 {
		t.Fatal("no vehicle rerouted around the closure")
	}
}

func TestInterventionsMoveTheRightMetrics(t *testing.T) {
	base := run(42, 8*3600) // 13:30
	type check struct {
		name   string
		iv     Intervention
		metric func(e *Engine) float64
	}
	pmIndustrial := func(e *Engine) float64 { return e.Latest().Districts[0].PM25 } // D1 Harbor Works
	checks := []check{
		{"rush hour", Intervention{DemandFactor: 1.5}, func(e *Engine) float64 { return float64(e.AgentCount()) }},
		{"heatwave", Intervention{TempDeltaC: 8}, func(e *Engine) float64 { return e.Latest().Summary.DemandMW }},
		{"industrial spike", Intervention{Emission: map[int]float64{0: 5}}, pmIndustrial},
	}
	for _, c := range checks {
		b, s := base.Clone(), base.Clone()
		if err := s.Apply(c.iv); err != nil {
			t.Fatal(err)
		}
		b.StepN(3600)
		s.StepN(3600)
		if mb, ms := c.metric(b), c.metric(s); ms <= mb {
			t.Errorf("%s: scenario %.1f not above baseline %.1f", c.name, ms, mb)
		}
	}
}

func TestAdaptiveSignalsReplan(t *testing.T) {
	e := run(42, 2*3600)
	before := e.GreenNS()
	if err := e.Apply(Intervention{SignalPolicy: SignalAdaptive}); err != nil {
		t.Fatal(err)
	}
	e.StepN(600)
	after := e.GreenNS()
	changed := 0
	for i, n := range e.City.Nodes {
		if n.Signalized {
			if after[i] != before[i] {
				changed++
			}
			if after[i] < n.CycleS/4 || after[i] > n.CycleS*3/4 {
				t.Fatalf("node %s green %d outside 25–75%% of a %d s cycle", n.ID, after[i], n.CycleS)
			}
		}
	}
	if changed == 0 {
		t.Fatal("adaptive policy did not change any signal plan")
	}
}

func TestApplyValidates(t *testing.T) {
	e := New(city.Generate(42))
	if e.Apply(Intervention{ClosedRoads: []int{9999}}) == nil {
		t.Fatal("accepted a road index out of range")
	}
	if e.Apply(Intervention{SignalPolicy: "chaos"}) == nil {
		t.Fatal("accepted an unknown signal policy")
	}
}
