package city

import (
	"fmt"
	"math"
	"math/rand/v2"
)

// Layout constants. The city is a 13×10 grid of intersections, 750 m apart,
// giving a 9 km × 6.75 km footprint split into 12 districts of 3×3 blocks.
const (
	GridCols   = 13
	GridRows   = 10
	SpacingM   = 750.0
	HighwayRow = 4 // Highway A runs east-west along this row
	HighwayCol = 6 // Highway B runs north-south along this column
	CellM      = 250.0
	Version    = "city-gen/1"
	jitterM    = 45.0
	removeFrac = 0.14 // share of local streets removed to create superblocks
)

var (
	districtColBounds = []int{0, 3, 6, 9, 12}
	districtRowBounds = []int{0, 3, 6, 9}

	arterialRows = map[int]string{1: "Harbor Avenue", 7: "Northern Avenue"}
	arterialCols = map[int]string{3: "Foundry Boulevard", 9: "Eastvale Boulevard"}
	rowStreets   = [GridRows]string{"Quay", "Mill", "Wharf", "Market", "", "Union", "Garden", "", "Ridge", "Summit"}
	colStreets   = [GridCols]string{"Ash", "Birch", "Cedar", "", "Elm", "Fir", "", "Hazel", "Ivy", "", "Juniper", "Larch", "Maple"}
)

type districtSpec struct{ name, typ string }

// districtLayout is indexed [row from south][column from west].
var districtLayout = [3][4]districtSpec{
	{{"Harbor Works", Industrial}, {"Foundry Row", Industrial}, {"Canal Market", Mixed}, {"Southgate", Residential}},
	{{"Westbrook", Residential}, {"Central Exchange", Commercial}, {"Meridian", Commercial}, {"Eastvale", Residential}},
	{{"Northfield", Residential}, {"Old Town", Mixed}, {"Highcliff", Residential}, {"Pinecrest", Residential}},
}

// Generate builds Nexus City from a seed. The same seed always produces an
// identical city.
func Generate(seed int64) *City {
	rng := rand.New(rand.NewPCG(uint64(seed), 0x5EEDC17))
	c := &City{
		Name:    "Nexus City",
		Version: Version,
		Seed:    seed,
		Origin:  Origin{Lon: -30.0, Lat: 20.0},
		WidthM:  float64(GridCols-1) * SpacingM,
		HeightM: float64(GridRows-1) * SpacingM,
	}
	c.buildDistricts(rng)
	c.buildNodes(rng)
	c.buildRoads(rng)
	c.assignSignals(rng)
	c.buildFacilities(rng)
	c.buildGrid()
	minLon, minLat := c.Origin.LonLat(0, 0)
	maxLon, maxLat := c.Origin.LonLat(c.WidthM, c.HeightM)
	c.Bounds = [4]float64{minLon, minLat, maxLon, maxLat}
	return c
}

func rowName(r int) string {
	if r == HighwayRow {
		return "Highway A"
	}
	if n, ok := arterialRows[r]; ok {
		return n
	}
	return rowStreets[r] + " Street"
}

func colName(c int) string {
	if c == HighwayCol {
		return "Highway B"
	}
	if n, ok := arterialCols[c]; ok {
		return n
	}
	return colStreets[c] + " Road"
}

func rowClass(r int) string {
	if r == HighwayRow {
		return Highway
	}
	if _, ok := arterialRows[r]; ok {
		return Arterial
	}
	return Local
}

func colClass(c int) string {
	if c == HighwayCol {
		return Highway
	}
	if _, ok := arterialCols[c]; ok {
		return Arterial
	}
	return Local
}

func between(rng *rand.Rand, lo, hi int) int { return lo + rng.IntN(hi-lo+1) }

func (c *City) buildDistricts(rng *rand.Rand) {
	for dr := 0; dr < 3; dr++ {
		for dc := 0; dc < 4; dc++ {
			spec := districtLayout[dr][dc]
			i := len(c.Districts)
			x0 := float64(districtColBounds[dc]) * SpacingM
			x1 := float64(districtColBounds[dc+1]) * SpacingM
			y0 := float64(districtRowBounds[dr]) * SpacingM
			y1 := float64(districtRowBounds[dr+1]) * SpacingM

			var pop, jobs int
			extraMW := 0.0
			switch spec.typ {
			case Residential:
				pop, jobs = between(rng, 38000, 58000), between(rng, 3000, 7000)
			case Commercial:
				pop, jobs = between(rng, 6000, 14000), between(rng, 45000, 70000)
			case Industrial:
				pop, jobs = between(rng, 2000, 6000), between(rng, 10000, 18000)
				extraMW = 18 + 8*rng.Float64() // process load
			case Mixed:
				pop, jobs = between(rng, 18000, 28000), between(rng, 14000, 24000)
			}
			floor := float64(pop)*38 + float64(jobs)*22
			base := float64(pop)*0.0006 + float64(jobs)*0.0009 + extraMW

			ring := [][2]float64{}
			for _, p := range [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}} {
				lon, lat := c.Origin.LonLat(p[0], p[1])
				ring = append(ring, [2]float64{lon, lat})
			}
			clon, clat := c.Origin.LonLat((x0+x1)/2, (y0+y1)/2)
			c.Districts = append(c.Districts, District{
				ID:              fmt.Sprintf("D%d", i+1),
				Index:           i,
				Name:            spec.name,
				Type:            spec.typ,
				X0:              x0,
				Y0:              y0,
				X1:              x1,
				Y1:              y1,
				Polygon:         ring,
				Centroid:        [2]float64{clon, clat},
				Population:      pop,
				Jobs:            jobs,
				FloorAreaM2:     math.Round(floor),
				BuildingDensity: round2(floor / ((x1 - x0) * (y1 - y0))),
				BaseLoadMW:      round2(base),
				// Sized so a hot evening peak runs close to, but usually under, capacity.
				SubstationMW: math.Round(base * (1.8 + 0.2*rng.Float64())),
			})
		}
	}
}

// DistrictAt returns the index of the district containing local point (x, y).
func (c *City) DistrictAt(x, y float64) int {
	dc := int(x / (3 * SpacingM))
	dr := int(y / (3 * SpacingM))
	dc = max(0, min(3, dc))
	dr = max(0, min(2, dr))
	return dr*4 + dc
}

func (c *City) buildNodes(rng *rand.Rand) {
	for r := 0; r < GridRows; r++ {
		for col := 0; col < GridCols; col++ {
			x := float64(col) * SpacingM
			y := float64(r) * SpacingM
			// Interior nodes get a little jitter so the grid looks organic.
			// Highways and arterials stay straight along their own axis.
			interior := r > 0 && r < GridRows-1 && col > 0 && col < GridCols-1
			dx := (rng.Float64()*2 - 1) * jitterM
			dy := (rng.Float64()*2 - 1) * jitterM
			if interior {
				if colClass(col) == Local {
					x += dx
				}
				if rowClass(r) == Local {
					y += dy
				}
			}
			lon, lat := c.Origin.LonLat(x, y)
			i := len(c.Nodes)
			d := c.DistrictAt(x, y)
			c.Nodes = append(c.Nodes, Node{
				ID:       fmt.Sprintf("N%d", i+1),
				Index:    i,
				Name:     rowName(r) + " & " + colName(col),
				Row:      r,
				Col:      col,
				X:        round2(x),
				Y:        round2(y),
				Lon:      lon,
				Lat:      lat,
				District: d,
			})
			c.Districts[d].Nodes = append(c.Districts[d].Nodes, i)
		}
	}
}

type roadCand struct {
	a, b              int
	axis, class, name string
	segment           string
	removed           bool
}

func (c *City) buildRoads(rng *rand.Rand) {
	var cands []roadCand
	for r := 0; r < GridRows; r++ {
		for col := 0; col < GridCols; col++ {
			n := r*GridCols + col
			if col+1 < GridCols {
				cands = append(cands, roadCand{a: n, b: n + 1, axis: "ew", class: rowClass(r), name: rowName(r),
					segment: colName(col) + " – " + colName(col+1)})
			}
			if r+1 < GridRows {
				cands = append(cands, roadCand{a: n, b: n + GridCols, axis: "ns", class: colClass(col), name: colName(col),
					segment: rowName(r) + " – " + rowName(r+1)})
			}
		}
	}

	// Remove some local streets to form superblocks, never disconnecting the
	// network or leaving a dead end.
	var local []int
	for i, cd := range cands {
		if cd.class == Local {
			local = append(local, i)
		}
	}
	rng.Shuffle(len(local), func(i, j int) { local[i], local[j] = local[j], local[i] })
	deg := make([]int, len(c.Nodes))
	for _, cd := range cands {
		deg[cd.a]++
		deg[cd.b]++
	}
	target := int(float64(len(local)) * removeFrac)
	removed := 0
	for _, i := range local {
		if removed >= target {
			break
		}
		cd := &cands[i]
		if deg[cd.a] <= 2 || deg[cd.b] <= 2 {
			continue
		}
		cd.removed = true
		if !candidatesConnected(len(c.Nodes), cands) {
			cd.removed = false
			continue
		}
		deg[cd.a]--
		deg[cd.b]--
		removed++
	}

	for _, cd := range cands {
		if cd.removed {
			continue
		}
		a, b := c.Nodes[cd.a], c.Nodes[cd.b]
		lanes, speed := 1, 30.0
		switch cd.class {
		case Highway:
			lanes, speed = 3, 80
		case Arterial:
			lanes, speed = 2, 50
		}
		district := c.DistrictAt((a.X+b.X)/2, (a.Y+b.Y)/2)
		if cd.class == Local && c.Districts[district].Type == Commercial {
			lanes = 2 // downtown streets are wider
		}
		i := len(c.Roads)
		c.Roads = append(c.Roads, Road{
			ID:       fmt.Sprintf("R%d", i+1),
			Index:    i,
			Name:     cd.name,
			Segment:  cd.segment,
			Class:    cd.class,
			A:        cd.a,
			B:        cd.b,
			Axis:     cd.axis,
			LengthM:  round2(math.Hypot(b.X-a.X, b.Y-a.Y)),
			Lanes:    lanes,
			SpeedKmh: speed,
			District: district,
			Coords:   [][2]float64{{a.Lon, a.Lat}, {b.Lon, b.Lat}},
		})
	}
	c.BuildAdjacency()
}

func candidatesConnected(n int, cands []roadCand) bool {
	adj := make([][]int, n)
	for _, cd := range cands {
		if !cd.removed {
			adj[cd.a] = append(adj[cd.a], cd.b)
			adj[cd.b] = append(adj[cd.b], cd.a)
		}
	}
	seen := make([]bool, n)
	stack := []int{0}
	seen[0] = true
	count := 1
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, v := range adj[u] {
			if !seen[v] {
				seen[v] = true
				count++
				stack = append(stack, v)
			}
		}
	}
	return count == n
}

func (c *City) assignSignals(rng *rand.Rand) {
	for i := range c.Nodes {
		links := c.adj[i]
		nonLocal := 0
		hwEW, hwNS := false, false
		for _, l := range links {
			road := c.Roads[l.Road]
			if road.Class != Local {
				nonLocal++
			}
			if road.Class == Highway {
				if road.Axis == "ew" {
					hwEW = true
				} else {
					hwNS = true
				}
			}
		}
		n := &c.Nodes[i]
		n.Major = hwEW || hwNS || nonLocal >= 3
		if len(links) < 3 || nonLocal == 0 {
			continue
		}
		// A highway crossing only local streets is grade-separated: local
		// streets pass under it, so there is no signal and no delay.
		hwOnly := (hwEW != hwNS) && nonLocal == 2
		if hwOnly {
			continue
		}
		n.Signalized = true
		n.CycleS = 90
		if hwEW || hwNS {
			n.CycleS = 120
		}
		n.GreenNSS = n.CycleS / 2
		switch {
		case hwEW && !hwNS: // favour the highway
			n.GreenNSS = n.CycleS * 40 / 100
		case hwNS && !hwEW:
			n.GreenNSS = n.CycleS * 60 / 100
		}
		n.OffsetS = rng.IntN(n.CycleS)
	}
}

// NearestNode returns the intersection closest to local point (x, y).
func (c *City) NearestNode(x, y float64) int {
	best, bestD := 0, math.Inf(1)
	for i, n := range c.Nodes {
		d := math.Hypot(n.X-x, n.Y-y)
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func (c *City) addFacility(kind, prefix, name string, x, y float64, capacity int) {
	count := 1
	for _, f := range c.Facilities {
		if f.Kind == kind {
			count++
		}
	}
	lon, lat := c.Origin.LonLat(x, y)
	c.Facilities = append(c.Facilities, Facility{
		ID:       fmt.Sprintf("%s%d", prefix, count),
		Kind:     kind,
		Name:     name,
		X:        round2(x),
		Y:        round2(y),
		Lon:      lon,
		Lat:      lat,
		District: c.DistrictAt(x, y),
		Node:     c.NearestNode(x, y),
		Capacity: capacity,
	})
}

// pointIn returns a point inside district d, at a fraction (fx, fy) of its
// width and height.
func (d District) pointIn(fx, fy float64) (float64, float64) {
	return d.X0 + (d.X1-d.X0)*fx, d.Y0 + (d.Y1-d.Y0)*fy
}

func (c *City) buildFacilities(rng *rand.Rand) {
	hospitals := []struct {
		name     string
		district int
		beds     int
	}{
		{"St. Aurelia General Hospital", 5, 520},
		{"Westbrook Medical Center", 4, 310},
		{"Northfield Community Hospital", 8, 220},
	}
	for _, h := range hospitals {
		x, y := c.Districts[h.district].pointIn(0.35+0.3*rng.Float64(), 0.35+0.3*rng.Float64())
		c.addFacility(Hospital, "H", h.name, x, y, h.beds)
	}
	for _, d := range c.Districts {
		if d.Type == Residential || d.Type == Mixed {
			x, y := d.pointIn(0.2+0.6*rng.Float64(), 0.2+0.6*rng.Float64())
			c.addFacility(School, "SC", d.Name+" Academy", x, y, between(rng, 600, 1800))
		}
	}
	for _, d := range c.Districts {
		x, y := d.pointIn(0.3+0.4*rng.Float64(), 0.3+0.4*rng.Float64())
		c.addFacility(AQStation, "AQ", d.Name+" Air Station", x, y, 0)
	}
	for _, d := range c.Districts {
		x, y := d.pointIn(0.15+0.7*rng.Float64(), 0.15+0.7*rng.Float64())
		c.addFacility(Substation, "SUB", d.Name+" Substation", x, y, int(d.SubstationMW))
	}
}

func (c *City) buildGrid() {
	cols := int(c.WidthM / CellM)
	rows := int(c.HeightM / CellM)
	g := Grid{CellM: CellM, Cols: cols, Rows: rows}
	for r := 0; r < rows; r++ {
		for col := 0; col < cols; col++ {
			lon, lat := c.Origin.LonLat((float64(col)+0.5)*CellM, (float64(r)+0.5)*CellM)
			g.Centers = append(g.Centers, [2]float64{lon, lat})
		}
	}
	c.Grid = g
}

// CellCenter returns the local coordinates of grid cell i.
func (c *City) CellCenter(i int) (float64, float64) {
	col := i % c.Grid.Cols
	r := i / c.Grid.Cols
	return (float64(col) + 0.5) * c.Grid.CellM, (float64(r) + 0.5) * c.Grid.CellM
}

// CellAt returns the grid cell containing local point (x, y).
func (c *City) CellAt(x, y float64) int {
	col := max(0, min(c.Grid.Cols-1, int(x/c.Grid.CellM)))
	r := max(0, min(c.Grid.Rows-1, int(y/c.Grid.CellM)))
	return r*c.Grid.Cols + col
}

// FacilitiesOf returns the indices of facilities of one kind, in ID order.
func (c *City) FacilitiesOf(kind string) []int {
	var out []int
	for i, f := range c.Facilities {
		if f.Kind == kind {
			out = append(out, i)
		}
	}
	return out
}
