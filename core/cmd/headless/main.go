// Command headless runs the simulation without Redis or a clock, as fast as
// the CPU allows. It can write a training dataset (CSV tables, the event
// log as labels, and a manifest), a telemetry JSON Lines dump, and prints
// an hourly summary. Phase 2 uses it to generate ML training data; Phase 3
// runs it as a Kubernetes Job for what-if scenarios.
package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/scenario"
	"nexuscity/internal/sim"
)

func main() {
	seed := flag.Int64("seed", 42, "city and simulation seed")
	hours := flag.Float64("hours", 24, "simulated hours to run (ignored if -days is set)")
	days := flag.Float64("days", 0, "simulated days to run")
	cityOut := flag.String("city-out", "", "write the city JSON to this path")
	telemetryOut := flag.String("telemetry-out", "", "write per-minute telemetry as JSON Lines to this path")
	telemetryEvery := flag.Int("telemetry-every-min", 1, "write only every N-th simulated minute to -telemetry-out")
	datasetDir := flag.String("dataset-dir", "", "write a training dataset (CSV tables + events + manifest) to this directory")
	sampleMin := flag.Int("sample-min", 5, "dataset sampling interval in simulated minutes")
	quiet := flag.Bool("quiet", false, "do not print the hourly summary")
	scenarioIn := flag.String("scenario", "", "run a what-if scenario from this spec JSON instead of a plain simulation")
	resultOut := flag.String("result-out", "", "with -scenario: write the result JSON here (default: stdout)")
	flag.Parse()

	if *scenarioIn != "" {
		runScenario(*scenarioIn, *resultOut)
		return
	}

	if *days > 0 {
		*hours = *days * 24
	}
	c := city.Generate(*seed)
	if *cityOut != "" {
		writeJSON(*cityOut, c)
	}

	var tw *bufio.Writer
	if *telemetryOut != "" {
		f, err := os.Create(*telemetryOut)
		check(err)
		defer f.Close()
		tw = bufio.NewWriterSize(f, 1<<20)
		defer tw.Flush()
	}
	var ds *dataset
	if *datasetDir != "" {
		ds = newDataset(*datasetDir, c, *sampleMin)
		defer ds.close()
	}

	e := sim.New(c)
	steps := int(*hours * 3600)
	start := time.Now()
	if !*quiet {
		fmt.Printf("%-16s %7s %6s %6s %7s %7s %6s %6s %5s %5s %5s\n",
			"sim time", "veh", "km/h", "cong", "MW", "util", "pm", "pmMax", "inc", "calls", "alert")
	}
	enc := json.NewEncoder(tw)
	flush := func() {
		for _, t := range e.DrainTelemetry() {
			if tw != nil && t.Tick%int64(*telemetryEvery*60) == 0 {
				check(enc.Encode(t))
			}
			if ds != nil {
				ds.add(t)
			}
			if !*quiet && t.Tick%3600 == 0 {
				s := t.Summary
				fmt.Printf("%-16s %7d %6.1f %6.3f %7.1f %7.3f %6.1f %6.1f %5d %5d %5d\n",
					t.SimTime.Format("Mon 01-02 15:04"), s.Vehicles, s.AvgSpeedKmh, s.AvgCongestion,
					s.DemandMW, s.DemandMW/s.CapacityMW, s.AvgPM25, s.MaxPM25, s.Incidents, s.Calls1h, s.Alerts)
			}
		}
		for _, ev := range e.DrainEvents() {
			if ds != nil {
				ds.event(ev)
			}
		}
	}
	flush() // tick-0 observation
	for i := 0; i < steps; i++ {
		e.Step()
		flush()
	}
	if ds != nil {
		ds.manifest(*seed, *hours, e.StateHash())
	}
	fmt.Fprintf(os.Stderr, "simulated %.1f h in %s (state %s)\n", *hours, time.Since(start).Round(time.Millisecond), e.StateHash()[:12])
}

// dataset writes long-format CSV tables sampled every sampleMin minutes.
// Rows reference entities by their index in city.json.
type dataset struct {
	dir                                  string
	city                                 *city.City
	sampleTicks                          int64
	rows                                 map[string]int
	roads, districts, stations, cityWide *csv.Writer
	files                                []*os.File
	events                               *bufio.Writer
	eventsEnc                            *json.Encoder
}

func newDataset(dir string, c *city.City, sampleMin int) *dataset {
	check(os.MkdirAll(dir, 0o755))
	writeJSON(filepath.Join(dir, "city.json"), c)
	d := &dataset{dir: dir, city: c, sampleTicks: int64(sampleMin) * 60, rows: map[string]int{}}
	open := func(name string, header ...string) *csv.Writer {
		f, err := os.Create(filepath.Join(dir, name))
		check(err)
		d.files = append(d.files, f)
		w := csv.NewWriter(f) // csv.Writer buffers internally; Flush writes to the file
		check(w.Write(header))
		return w
	}
	d.roads = open("roads.csv", "tick", "road", "flow_vph", "speed_kmh", "congestion", "vehicles", "incident")
	d.districts = open("districts.csv", "tick", "district", "demand_mw", "utilization", "pm25", "vehicles", "calls_1h", "avg_eta_min")
	d.stations = open("stations.csv", "tick", "station", "pm25", "status")
	d.cityWide = open("city.csv", "tick", "temp_c", "humidity", "wind_speed_ms", "wind_dir_deg",
		"vehicles", "avg_speed_kmh", "avg_congestion", "demand_mw", "avg_pm25", "incidents")
	f, err := os.Create(filepath.Join(dir, "events.jsonl"))
	check(err)
	d.files = append(d.files, f)
	d.events = bufio.NewWriter(f)
	d.eventsEnc = json.NewEncoder(d.events)
	return d
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
func fi(v int) string     { return strconv.Itoa(v) }

func (d *dataset) add(t *events.Telemetry) {
	if t.Tick%d.sampleTicks != 0 {
		return
	}
	tick := strconv.FormatInt(t.Tick, 10)
	for i, o := range t.Roads {
		inc := "0"
		if o.Incident != "" {
			inc = "1"
		}
		check(d.roads.Write([]string{tick, fi(i), ff(o.FlowVph), ff(o.SpeedKmh), ff(o.Congestion), fi(o.Vehicles), inc}))
	}
	for i, o := range t.Districts {
		check(d.districts.Write([]string{tick, fi(i), ff(o.DemandMW), ff(o.Utilization), ff(o.PM25), fi(o.Vehicles), fi(o.Calls1h), ff(o.AvgEtaMin)}))
	}
	for i, o := range t.Stations {
		pm := "" // missing reading
		if o.PM25 != nil {
			pm = ff(*o.PM25)
		}
		check(d.stations.Write([]string{tick, fi(i), pm, o.Status}))
	}
	w, s := t.Weather, t.Summary
	check(d.cityWide.Write([]string{tick, ff(w.TempC), ff(w.Humidity), ff(w.WindSpeed), ff(w.WindDirDeg),
		fi(s.Vehicles), ff(s.AvgSpeedKmh), ff(s.AvgCongestion), ff(s.DemandMW), ff(s.AvgPM25), fi(s.Incidents)}))
	d.rows["samples"]++
}

func (d *dataset) event(ev events.Event) {
	tick := int64(ev.SimTime.Sub(sim.Epoch).Seconds())
	check(d.eventsEnc.Encode(struct {
		Tick int64 `json:"tick"`
		events.Event
	}{tick, ev}))
	d.rows["events"]++
}

func (d *dataset) manifest(seed int64, hours float64, stateHash string) {
	writeJSON(filepath.Join(d.dir, "manifest.json"), map[string]any{
		"seed":             seed,
		"hours":            hours,
		"sample_minutes":   d.sampleTicks / 60,
		"epoch":            sim.Epoch,
		"city_version":     city.Version,
		"agent_scale":      sim.AgentScale,
		"samples":          d.rows["samples"],
		"events":           d.rows["events"],
		"final_state_hash": stateHash,
		"generated_at":     time.Now().UTC(),
		"tables":           []string{"roads.csv", "districts.csv", "stations.csv", "city.csv", "events.jsonl"},
		"note":             "Synthetic data from the Nexus City simulation; not real-world observations.",
	})
}

func (d *dataset) close() {
	for _, w := range []*csv.Writer{d.roads, d.districts, d.stations, d.cityWide} {
		w.Flush()
		check(w.Error())
	}
	check(d.events.Flush())
	for _, f := range d.files {
		f.Close()
	}
}

func writeJSON(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	check(err)
	check(os.WriteFile(path, b, 0o644))
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// runScenario executes one what-if scenario spec (see internal/scenario).
// This is also the entry point for scenario Jobs in Kubernetes (Phase 5).
func runScenario(specPath, out string) {
	raw, err := os.ReadFile(specPath)
	check(err)
	var spec scenario.Spec
	check(json.Unmarshal(raw, &spec))
	res, _, err := scenario.Run(spec, func(phase string, f float64) {
		fmt.Fprintf(os.Stderr, "%-20s %3.0f%%", phase, f*100)
	})
	fmt.Fprintln(os.Stderr)
	check(err)
	if out == "" {
		check(json.NewEncoder(os.Stdout).Encode(res))
		return
	}
	writeJSON(out, res)
}
