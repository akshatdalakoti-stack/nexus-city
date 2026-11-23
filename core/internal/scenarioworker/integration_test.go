package scenarioworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"nexuscity/internal/events"
	"nexuscity/internal/gateway"
	"nexuscity/internal/simworker"
	"nexuscity/internal/store"
)

// fakeModels stands in for the Python services: it answers /predict/{model}
// with a forecast whose values depend on the request, so baseline and
// scenario forecasts differ when their telemetry differs.
func fakeModels(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := strings.TrimPrefix(r.URL.Path, "/predict/")
		var body struct {
			Telemetry []struct {
				Tick    int64            `json:"tick"`
				Summary events.Summary   `json:"summary"`
				Roads   []events.RoadObs `json:"roads"`
			} `json:"telemetry"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Telemetry) == 0 {
			http.Error(w, "bad request", 422)
			return
		}
		last := body.Telemetry[len(body.Telemetry)-1]
		f := events.Forecast{Model: model, Version: model + "-test", IssuedTick: last.Tick, Values: map[string][]float64{}}
		switch model {
		case "traffic":
			f.EntityType, f.Horizons = "road", []int{15, 30, 60}
			for _, o := range last.Roads {
				f.Entities = append(f.Entities, o.ID)
			}
			for _, h := range f.Horizons {
				vals := make([]float64, len(last.Roads))
				for i, o := range last.Roads {
					vals[i] = o.Congestion
				}
				f.Values[jsonKey(h)] = vals
			}
		case "energy":
			f.EntityType, f.Entities = "district", []string{"D1"}
			for h := 1; h <= 24; h++ {
				f.Horizons = append(f.Horizons, h)
				f.Values[jsonKey(h)] = []float64{last.Summary.DemandMW}
			}
		case "pm25":
			f.EntityType, f.Entities, f.Horizons = "station", []string{"AQ1"}, []int{30, 60}
			f.Values["30"], f.Values["60"] = []float64{last.Summary.AvgPM25}, []float64{last.Summary.AvgPM25}
		}
		for _, h := range f.Horizons {
			f.TargetTicks = append(f.TargetTicks, last.Tick+int64(h)*60)
		}
		_ = json.NewEncoder(w).Encode(f)
	}))
}

func jsonKey(h int) string { b, _ := json.Marshal(h); return string(b) }

func TestCompleteScenarioExecution(t *testing.T) {
	url := os.Getenv("NEXUS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NEXUS_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	// A live run to branch from: 07:30 on the first weekday.
	simCfg := simworker.DefaultConfig()
	simCfg.Speed = 60
	sim := simworker.New(simCfg, rdb)
	if err := sim.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 120; i++ {
		now = now.Add(time.Second)
		sim.Advance(ctx, time.Second, now)
	}

	gw := gateway.New(gateway.Config{Version: "test", Store: db}, rdb)
	go gw.Consume(ctx)
	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()
	deadline := time.Now().Add(5 * time.Second)
	for gw.State().RunID() != sim.RunID() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	models := fakeModels(t)
	defer models.Close()
	w := New(Config{Consumer: "test-worker", ModelURLs: ParseModelURLs(models.URL)}, rdb, db)
	go func() { _ = w.Run(ctx) }()

	post := func(body string) (int, map[string]any) {
		res, err := http.Post(ts.URL+"/api/scenarios", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := post(`{"kind":"road_closure","params":{"road_ids":["R9999"]},"duration_min":60}`); code != 400 {
		t.Fatalf("invalid road accepted: %d", code)
	}
	if code, _ := post(`{"kind":"road_closure","params":{"road_ids":["R102"]},"duration_min":60,"surprise":1}`); code != 400 {
		t.Fatalf("unknown field accepted: %d", code)
	}
	code, created := post(`{"kind":"road_closure","params":{"road_ids":["R102"]},"duration_min":60}`)
	if code != http.StatusAccepted {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["id"].(string)
	if created["base_tick"].(float64) != 7200 {
		t.Fatalf("should branch from the live time rounded to 5 min, got %v", created["base_tick"])
	}

	var sc store.Scenario
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(ts.URL + "/api/scenarios/" + id)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(res.Body).Decode(&sc)
		res.Body.Close()
		if sc.Status == "done" || sc.Status == "failed" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sc.Status != "done" {
		t.Fatalf("scenario ended %q: %v", sc.Status, sc.Error)
	}
	var result struct {
		ResultHash string `json:"result_hash"`
		Scenario   struct {
			Rerouted int `json:"rerouted"`
		} `json:"scenario"`
		Roads []struct {
			ID     string `json:"id"`
			Closed bool   `json:"closed"`
		} `json:"roads"`
		Context struct {
			NearbyRoads []struct{ ID string } `json:"nearby_roads"`
		} `json:"context"`
		Forecasts ForecastComparison `json:"forecasts"`
	}
	if err := json.Unmarshal(sc.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ResultHash == "" || *sc.ResultHash != result.ResultHash {
		t.Fatal("result hash missing")
	}
	if len(result.Context.NearbyRoads) == 0 {
		t.Fatal("PostGIS context missing nearby roads")
	}
	if result.Forecasts.Traffic == nil || result.Forecasts.Energy == nil || result.Forecasts.PM25 == nil || len(result.Forecasts.Errors) != 0 {
		t.Fatalf("forecasts incomplete: %+v", result.Forecasts)
	}
	if result.Forecasts.Energy.BaselineMWh == 0 {
		t.Fatal("energy forecast comparison empty")
	}

	// Reproducibility end to end: the same spec again gives the same outcome.
	code, again := post(`{"kind":"road_closure","params":{"road_ids":["R102"]},"duration_min":60,"base_tick":7200}`)
	if code != http.StatusAccepted || again["spec_hash"] != created["spec_hash"] {
		t.Fatalf("same spec should hash the same: %v vs %v", again["spec_hash"], created["spec_hash"])
	}
	var sc2 store.Scenario
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		sc2p, _ := db.GetScenario(ctx, again["id"].(string))
		if sc2p != nil && sc2p.Status == "done" {
			sc2 = *sc2p
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sc2.ResultHash == nil || *sc2.ResultHash != result.ResultHash {
		t.Fatalf("rerun produced a different result: %v vs %s", sc2.ResultHash, result.ResultHash)
	}

	res, _ := http.Get(ts.URL + "/api/scenarios/queue")
	var q gateway.QueueInfo
	_ = json.NewDecoder(res.Body).Decode(&q)
	res.Body.Close()
	// miniredis does not compute XINFO GROUPS lag like Redis 7; waiting
	// counts are checked against a real Redis in the end-to-end run.
	if q.Consumers != 1 || q.Running != 0 {
		t.Fatalf("queue after processing: %+v", q)
	}
}

func TestParseModelURLs(t *testing.T) {
	one := ParseModelURLs("http://ml:8090/")
	if one["traffic"] != "http://ml:8090" || one["pm25"] != "http://ml:8090" {
		t.Fatalf("%v", one)
	}
	many := ParseModelURLs("traffic=http://a:1, energy=http://b:2")
	if many["traffic"] != "http://a:1" || many["energy"] != "http://b:2" || many["pm25"] != "" {
		t.Fatalf("%v", many)
	}
}
