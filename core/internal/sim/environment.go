package sim

import (
	"math"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

// Pollution model constants. The model is a simple Gaussian-kernel
// dispersion of traffic and industrial emissions, shifted downwind and
// diluted by wind speed, with first-order relaxation in time. It is a
// plausible-looking demonstration, not an air-quality model.
const (
	emissionPerVehKm  = 0.0065 // PM2.5 source strength per vehicle-km/h
	stackStrength     = 30.0   // per industrial stack at full activity
	plumeSigmaM       = 380.0
	pmRelaxMinutes    = 25.0
	stationNoise      = 0.04
	faultChancePerMin = 0.0007
)

type stack struct {
	x, y     float64
	district int
}

type stationState struct {
	facility   int
	cell       int
	status     string // ok | stuck | offline
	faultUntil int64
	last       float64
}

func (e *Engine) initEnvironment() {
	c := e.City
	for i := range e.envPhase {
		e.envPhase[i] = e.rEnv.Float64() * 2 * math.Pi
	}
	e.updateWeather()
	n := c.Grid.Cols * c.Grid.Rows
	e.pm = make([]float64, n)
	e.cellDist = make([]int, n)
	bg := e.background()
	for i := range e.pm {
		e.pm[i] = bg
		x, y := c.CellCenter(i)
		e.cellDist[i] = c.DistrictAt(x, y)
	}
	for _, d := range c.Districts {
		if d.Type != city.Industrial {
			continue
		}
		for k := 0; k < 3; k++ {
			x, y := d.X0+(d.X1-d.X0)*(0.2+0.6*e.rEnv.Float64()), d.Y0+(d.Y1-d.Y0)*(0.2+0.6*e.rEnv.Float64())
			e.stacks = append(e.stacks, stack{x: x, y: y, district: d.Index})
		}
	}
	for _, fi := range c.FacilitiesOf(city.AQStation) {
		f := c.Facilities[fi]
		e.stations = append(e.stations, stationState{facility: fi, cell: c.CellAt(f.X, f.Y), status: "ok", last: bg})
	}
}

// updateWeather sets weather as a smooth deterministic function of time:
// a diurnal temperature cycle on top of multi-day swings, with slowly
// veering wind.
func (e *Engine) updateWeather() {
	t := e.SimTime()
	p := e.envPhase
	day := float64(e.Tick)/86400 + 5.5/24 // days since midnight before the epoch
	h := hourOf(t)
	dayOffset := 2.4*math.Sin(2*math.Pi*day/5.3+p[0]) + 1.2*math.Sin(2*math.Pi*day/2.1+p[1])
	temp := 27.5 + dayOffset + 5.5*math.Sin(2*math.Pi*(h-9)/24) + e.iv.tempDelta
	hum := 68 - 2.2*(temp-27.5) + 6*math.Sin(2*math.Pi*day/3.3+p[2])
	wind := 3.0 + 1.5*math.Sin(2*math.Pi*day/3.7+p[3]) + 0.9*math.Sin(2*math.Pi*(h-10)/24)
	dir := 225 + 70*math.Sin(2*math.Pi*day/2.6+p[4]) + 25*math.Sin(2*math.Pi*h/12+p[5])
	e.weather = events.Weather{
		TempC:      round1(temp),
		Humidity:   round1(math.Max(22, math.Min(96, hum))),
		WindSpeed:  round1(math.Max(0.4, wind)),
		WindDirDeg: math.Round(math.Mod(dir+360, 360)),
	}
}

func (e *Engine) background() float64 {
	return 8 + 0.06*(e.weather.Humidity-50)
}

// industrialActivity is the share of full output the plants run at.
func industrialActivity(h float64, weekend bool) float64 {
	a := 0.55 + 0.45*plateau(h, 6, 22)
	if weekend {
		a *= 0.8
	}
	return a
}

func plateau(h, start, end float64) float64 {
	return sigmoid(2*(h-start)) * sigmoid(2*(end-h))
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

type source struct{ x, y, s float64 }

// updatePollution recomputes the PM2.5 field once per simulated minute.
func (e *Engine) updatePollution(roads []events.RoadObs) {
	c := e.City
	w := e.weather
	toward := (w.WindDirDeg + 180) * math.Pi / 180
	shift := math.Min(700, w.WindSpeed*110)
	sx, sy := math.Sin(toward)*shift, math.Cos(toward)*shift
	dilution := 1 / (0.55 + 0.32*w.WindSpeed)

	srcs := make([]source, 0, len(roads)+len(e.stacks))
	for i, o := range roads {
		if o.FlowVph <= 0 {
			continue
		}
		road := &c.Roads[i]
		a, b := c.Nodes[road.A], c.Nodes[road.B]
		s := o.FlowVph * road.LengthM / 1000 * emissionPerVehKm * (1 + 1.2*o.Congestion)
		srcs = append(srcs, source{x: (a.X+b.X)/2 + sx, y: (a.Y+b.Y)/2 + sy, s: s})
	}
	t := e.SimTime()
	act := industrialActivity(hourOf(t), isWeekend(t))
	for i, st := range e.stacks {
		srcs = append(srcs, source{x: st.x + sx*1.5, y: st.y + sy*1.5, s: stackStrength * act * e.releaseFactor(i) * e.iv.emission[st.district]})
	}

	inv := 1 / (2 * plumeSigmaM * plumeSigmaM)
	cutoff := 9 * plumeSigmaM * plumeSigmaM
	alpha := 1 - math.Exp(-1/pmRelaxMinutes)
	bg := e.background()
	for i := range e.pm {
		x, y := c.CellCenter(i)
		sum := 0.0
		for _, s := range srcs {
			dx, dy := x-s.x, y-s.y
			d2 := dx*dx + dy*dy
			if d2 < cutoff {
				sum += s.s * math.Exp(-d2*inv)
			}
		}
		target := bg + dilution*sum
		e.pm[i] += (target - e.pm[i]) * alpha
	}
}

// updateStations reads the field at each air-quality station, including
// injected sensor faults (stuck readings and dropouts).
func (e *Engine) updateStations() {
	for i := range e.stations {
		s := &e.stations[i]
		f := e.City.Facilities[s.facility]
		if s.status != "ok" && e.Tick >= s.faultUntil {
			s.status = "ok"
			e.emit(events.Event{Kind: "sensor.recovered", Severity: "info", EntityType: "station", EntityID: f.ID,
				Message: f.Name + " back online"})
		} else if s.status == "ok" && e.rFault.Float64() < faultChancePerMin {
			s.status = "offline"
			if e.rFault.Float64() < 0.55 {
				s.status = "stuck"
			}
			s.faultUntil = e.Tick + int64(20+e.rFault.IntN(70))*60
			e.emit(events.Event{Kind: "sensor.fault", Severity: "warning", EntityType: "station", EntityID: f.ID,
				Message: f.Name + " sensor " + s.status, Data: map[string]any{"fault": s.status}})
		}
		reading := round1(e.pm[s.cell] * (1 + stationNoise*e.rEnv.NormFloat64()))
		if s.status == "ok" {
			s.last = reading
		}
	}
}

func (e *Engine) stationObs() []events.StationObs {
	out := make([]events.StationObs, len(e.stations))
	for i, s := range e.stations {
		o := events.StationObs{ID: e.City.Facilities[s.facility].ID, Status: s.status}
		if s.status != "offline" {
			v := s.last
			o.PM25 = &v
		}
		out[i] = o
	}
	return out
}
