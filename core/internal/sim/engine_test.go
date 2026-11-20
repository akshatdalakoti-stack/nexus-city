package sim

import (
	"encoding/json"
	"testing"

	"nexuscity/internal/city"
)

func run(seed int64, steps int) *Engine {
	e := New(city.Generate(seed))
	e.StepN(steps)
	return e
}

func TestSameSeedSameState(t *testing.T) {
	const steps = 3 * 3600 // three simulated hours, through the morning peak
	a, b := run(42, steps), run(42, steps)
	if a.StateHash() != b.StateHash() {
		t.Fatal("same seed diverged")
	}
	ja, _ := json.Marshal(a.Latest())
	jb, _ := json.Marshal(b.Latest())
	if string(ja) != string(jb) {
		t.Fatal("same seed produced different telemetry")
	}
}

func TestDifferentSeedsDiffer(t *testing.T) {
	if run(1, 1800).StateHash() == run(2, 1800).StateHash() {
		t.Fatal("different seeds produced the same state")
	}
}

// Reading results between steps (as the live worker does) must not change
// the outcome.
func TestObservationDoesNotPerturb(t *testing.T) {
	a := New(city.Generate(42))
	b := New(city.Generate(42))
	for i := 0; i < 7200; i++ {
		a.Step()
		b.Step()
		if i%17 == 0 {
			b.Frame()
			b.DrainEvents()
			b.DrainTelemetry()
		}
	}
	if a.StateHash() != b.StateHash() {
		t.Fatal("reading frames and draining outputs changed the simulation")
	}
}

func TestVehicleAccountingIsConsistent(t *testing.T) {
	e := run(42, 2*3600)
	total := 0
	for _, rs := range e.roads {
		if rs.count[0] < 0 || rs.count[1] < 0 {
			t.Fatal("negative road occupancy")
		}
		total += rs.count[0] + rs.count[1]
	}
	if total != len(e.vehicles) {
		t.Fatalf("roads hold %d agents, engine has %d", total, len(e.vehicles))
	}
	if len(e.vehicles) == 0 {
		t.Fatal("no traffic at 07:30 on a weekday")
	}
}

func TestTelemetryShape(t *testing.T) {
	e := run(42, 3600)
	tel := e.Latest()
	c := e.City
	if tel.Tick != 3600 || tel.Tick%60 != 0 {
		t.Fatalf("tick %d", tel.Tick)
	}
	if len(tel.Roads) != len(c.Roads) || len(tel.Districts) != len(c.Districts) {
		t.Fatal("telemetry arrays not aligned with the city")
	}
	if len(tel.PMGrid) != c.Grid.Cols*c.Grid.Rows {
		t.Fatal("pm grid size mismatch")
	}
	for i, o := range tel.Roads {
		if o.ID != c.Roads[i].ID {
			t.Fatalf("road %d id %s, want %s", i, o.ID, c.Roads[i].ID)
		}
		if o.Congestion < 0 || o.Congestion > 1 || o.SpeedKmh <= 0 || o.SpeedKmh > c.Roads[i].SpeedKmh+0.1 {
			t.Fatalf("road %s implausible: %+v", o.ID, o)
		}
	}
	for _, st := range tel.Stations {
		if st.Status == "offline" && st.PM25 != nil {
			t.Fatal("offline station reported a value")
		}
		if st.Status == "ok" && (st.PM25 == nil || *st.PM25 <= 0) {
			t.Fatal("healthy station missing a reading")
		}
	}
	if tel.Summary.DemandMW <= 0 || tel.Summary.CapacityMW <= tel.Summary.DemandMW {
		t.Fatalf("energy summary implausible: %+v", tel.Summary)
	}
}

func TestOneTelemetryPerMinute(t *testing.T) {
	e := New(city.Generate(42))
	e.DrainTelemetry() // tick-0 observation
	e.StepN(600)
	got := e.DrainTelemetry()
	if len(got) != 10 {
		t.Fatalf("got %d observations in 10 minutes", len(got))
	}
	for i, tel := range got {
		if tel.Tick != int64(60*(i+1)) {
			t.Fatalf("observation %d at tick %d", i, tel.Tick)
		}
	}
}

func TestFrameMatchesVehicles(t *testing.T) {
	e := run(42, 5400)
	f := e.Frame()
	if len(f.Vehicles) != e.AgentCount() {
		t.Fatal("frame vehicle count mismatch")
	}
	b := e.City.Bounds
	for _, v := range f.Vehicles {
		if v[1] < b[0]-0.001 || v[1] > b[2]+0.001 || v[2] < b[1]-0.001 || v[2] > b[3]+0.001 {
			t.Fatalf("vehicle %v outside the city", v)
		}
	}
}

func BenchmarkSimulatedMinute(b *testing.B) {
	e := run(42, 3*3600)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.StepN(60)
	}
}
