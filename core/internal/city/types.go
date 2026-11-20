// Package city generates the static, reproducible layout of Nexus City:
// districts, the road graph, intersections with signal timings, facilities
// and the pollution grid. Everything here is a pure function of the seed.
package city

import "math"

// District types.
const (
	Residential = "residential"
	Commercial  = "commercial"
	Industrial  = "industrial"
	Mixed       = "mixed"
)

// Road classes.
const (
	Highway  = "highway"
	Arterial = "arterial"
	Local    = "local"
)

// Facility kinds.
const (
	Hospital   = "hospital"
	School     = "school"
	AQStation  = "aq_station"
	Substation = "substation"
)

// Origin anchors the city's local metre grid to longitude/latitude so the
// map can draw it. The city is fictional; the anchor is open ocean.
type Origin struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
}

const metersPerDegLat = 111320.0

// LonLat converts local metres (x east, y north from the south-west corner)
// to longitude/latitude using an equirectangular approximation, which is
// accurate to well under a metre over a 10 km city.
func (o Origin) LonLat(x, y float64) (float64, float64) {
	lat := o.Lat + y/metersPerDegLat
	lon := o.Lon + x/(metersPerDegLat*math.Cos(o.Lat*math.Pi/180))
	return round6(lon), round6(lat)
}

// Node is a road intersection.
type Node struct {
	ID         string  `json:"id"`
	Index      int     `json:"index"`
	Name       string  `json:"name"`
	Row        int     `json:"row"`
	Col        int     `json:"col"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Lon        float64 `json:"lon"`
	Lat        float64 `json:"lat"`
	District   int     `json:"district"`
	Signalized bool    `json:"signalized"`
	Major      bool    `json:"major"`
	// Signal plan, in whole seconds so the simulation stays exact.
	CycleS   int `json:"cycle_s,omitempty"`
	GreenNSS int `json:"green_ns_s,omitempty"` // north-south green time per cycle
	OffsetS  int `json:"offset_s,omitempty"`
}

// Road is an undirected road segment between two adjacent intersections.
// Traffic is simulated separately in each direction (A→B is dir 0).
type Road struct {
	ID       string       `json:"id"`
	Index    int          `json:"index"`
	Name     string       `json:"name"`
	Segment  string       `json:"segment"` // the two cross streets it runs between
	Class    string       `json:"class"`
	A        int          `json:"a"`
	B        int          `json:"b"`
	Axis     string       `json:"axis"` // "ew" or "ns", used by signal phases
	LengthM  float64      `json:"length_m"`
	Lanes    int          `json:"lanes"` // per direction
	SpeedKmh float64      `json:"speed_kmh"`
	District int          `json:"district"`
	Coords   [][2]float64 `json:"coords"` // [[lon,lat],[lon,lat]] from A to B
}

// District is a rectangular zone with its own land use and substation.
type District struct {
	ID              string       `json:"id"`
	Index           int          `json:"index"`
	Name            string       `json:"name"`
	Type            string       `json:"type"`
	X0              float64      `json:"x0"`
	Y0              float64      `json:"y0"`
	X1              float64      `json:"x1"`
	Y1              float64      `json:"y1"`
	Polygon         [][2]float64 `json:"polygon"` // closed lon/lat ring
	Centroid        [2]float64   `json:"centroid"`
	Population      int          `json:"population"`
	Jobs            int          `json:"jobs"`
	FloorAreaM2     float64      `json:"floor_area_m2"`
	BuildingDensity float64      `json:"building_density"` // floor-area ratio
	BaseLoadMW      float64      `json:"base_load_mw"`
	SubstationMW    float64      `json:"substation_mw"`
	Nodes           []int        `json:"nodes"`
}

// Facility is a point of interest: hospital, school, air-quality station
// or substation.
type Facility struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Lon      float64 `json:"lon"`
	Lat      float64 `json:"lat"`
	District int     `json:"district"`
	Node     int     `json:"node"` // nearest intersection, for routing
	Capacity int     `json:"capacity,omitempty"`
}

// Grid is the raster the pollution field is computed on.
type Grid struct {
	CellM   float64      `json:"cell_m"`
	Cols    int          `json:"cols"`
	Rows    int          `json:"rows"`
	Centers [][2]float64 `json:"centers"` // lon/lat, row-major from the south-west
}

// City is the full static description of Nexus City.
type City struct {
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	Seed       int64      `json:"seed"`
	Origin     Origin     `json:"origin"`
	WidthM     float64    `json:"width_m"`
	HeightM    float64    `json:"height_m"`
	Bounds     [4]float64 `json:"bounds"` // minLon, minLat, maxLon, maxLat
	Nodes      []Node     `json:"nodes"`
	Roads      []Road     `json:"roads"`
	Districts  []District `json:"districts"`
	Facilities []Facility `json:"facilities"`
	Grid       Grid       `json:"grid"`

	adj [][]Link
}

// Link is one way of leaving a node: along Road, arriving at To.
// Dir is 0 when travelling the road A→B and 1 when travelling B→A.
type Link struct {
	Road int
	To   int
	Dir  int
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
