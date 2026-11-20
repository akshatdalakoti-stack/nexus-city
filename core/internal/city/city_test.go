package city

import (
	"encoding/json"
	"math"
	"testing"
)

func TestGenerateIsDeterministic(t *testing.T) {
	a, _ := json.Marshal(Generate(42))
	b, _ := json.Marshal(Generate(42))
	if string(a) != string(b) {
		t.Fatal("same seed produced different cities")
	}
	c, _ := json.Marshal(Generate(43))
	if string(a) == string(c) {
		t.Fatal("different seeds produced identical cities")
	}
}

func TestRoadGraphIsConnected(t *testing.T) {
	for _, seed := range []int64{1, 42, 99, 2026} {
		c := Generate(seed)
		if !c.Connected() {
			t.Fatalf("seed %d: road network is disconnected", seed)
		}
		for i := range c.Nodes {
			if len(c.Links(i)) < 2 {
				t.Fatalf("seed %d: node %s is a dead end", seed, c.Nodes[i].ID)
			}
		}
	}
}

func TestCityStructure(t *testing.T) {
	c := Generate(42)
	if len(c.Nodes) != GridCols*GridRows {
		t.Fatalf("nodes = %d", len(c.Nodes))
	}
	if len(c.Districts) != 12 {
		t.Fatalf("districts = %d", len(c.Districts))
	}
	counts := map[string]int{}
	for _, f := range c.Facilities {
		counts[f.Kind]++
	}
	if counts[Hospital] != 3 || counts[AQStation] != 12 || counts[Substation] != 12 || counts[School] == 0 {
		t.Fatalf("facility counts %v", counts)
	}
	classes := map[string]int{}
	for _, r := range c.Roads {
		classes[r.Class]++
		if r.LengthM < 600 || r.LengthM > 900 {
			t.Fatalf("road %s has implausible length %.0f", r.ID, r.LengthM)
		}
	}
	if classes[Highway] != (GridCols-1)+(GridRows-1) {
		t.Fatalf("highway segments = %d", classes[Highway])
	}
	for _, d := range c.Districts {
		if len(d.Nodes) == 0 {
			t.Fatalf("district %s has no intersections", d.ID)
		}
		if d.SubstationMW <= d.BaseLoadMW {
			t.Fatalf("district %s substation smaller than base load", d.ID)
		}
	}
	for _, n := range c.Nodes {
		if n.Signalized && (n.CycleS <= 0 || n.GreenNSS <= 0 || n.GreenNSS >= n.CycleS || n.OffsetS >= n.CycleS) {
			t.Fatalf("node %s has invalid signal plan %+v", n.ID, n)
		}
	}
	if len(c.Grid.Centers) != c.Grid.Cols*c.Grid.Rows {
		t.Fatal("grid centers do not match grid size")
	}
}

func TestShortestPathIsContiguous(t *testing.T) {
	c := Generate(42)
	cost := func(r int) float64 { return c.Roads[r].LengthM }
	src, dst := 0, len(c.Nodes)-1
	path, total, ok := c.ShortestPath(src, dst, cost, nil)
	if !ok || len(path) == 0 {
		t.Fatal("no path between opposite corners")
	}
	at, sum := src, 0.0
	for _, l := range path {
		r := c.Roads[l.Road]
		from := r.A
		if l.Dir == 1 {
			from = r.B
		}
		if from != at {
			t.Fatalf("path breaks at road %s", r.ID)
		}
		at = l.To
		sum += r.LengthM
	}
	if at != dst {
		t.Fatalf("path ends at %d, want %d", at, dst)
	}
	if math.Abs(sum-total) > 1e-6 {
		t.Fatalf("reported cost %.1f, summed %.1f", total, sum)
	}
	// Manhattan lower bound: the corners are 12 and 9 blocks apart.
	if total < (12+9)*SpacingM*0.9 {
		t.Fatalf("path shorter than physically possible: %.0f m", total)
	}
}

func TestClosedRoadIsAvoided(t *testing.T) {
	c := Generate(42)
	base := func(r int) float64 { return c.Roads[r].LengthM }
	path, _, _ := c.ShortestPath(0, len(c.Nodes)-1, base, nil)
	closed := path[len(path)/2].Road
	cost := func(r int) float64 {
		if r == closed {
			return math.Inf(1)
		}
		return base(r)
	}
	alt, _, ok := c.ShortestPath(0, len(c.Nodes)-1, cost, nil)
	if !ok {
		t.Fatal("closing one road disconnected the corners")
	}
	for _, l := range alt {
		if l.Road == closed {
			t.Fatal("route uses a closed road")
		}
	}
}
