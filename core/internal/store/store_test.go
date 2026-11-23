package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"nexuscity/internal/city"
	"nexuscity/internal/sim"
)

// These tests need a PostGIS database, e.g. the development container:
//
//	NEXUS_TEST_DATABASE_URL=postgres://nexus:nexus-dev@localhost:5433/nexus_test
//
// The schema is dropped and recreated, so never point this at real data.
func openTest(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("NEXUS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NEXUS_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMigrationsAreIdempotent(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal("second migrate failed:", err)
	}
	var n int
	s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d migrations recorded", n)
	}
}

func TestCityGeometryAndSpatialQueries(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	c := city.Generate(42)
	id, err := s.EnsureCity(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.EnsureCity(ctx, c)
	if err != nil || again != id {
		t.Fatalf("EnsureCity not idempotent: %d vs %d (%v)", id, again, err)
	}
	var roads, valid int
	s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE ST_IsValid(geom)) FROM road_segments WHERE city_id = $1`, id).Scan(&roads, &valid)
	if roads != len(c.Roads) || valid != roads {
		t.Fatalf("%d roads stored (%d valid), want %d", roads, valid, len(c.Roads))
	}
	// A road's direct neighbours share an intersection, so they are 0 m away.
	r := c.Roads[0]
	near, err := s.RoadsNear(ctx, id, []string{r.ID}, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := len(c.Links(r.A)) + len(c.Links(r.B)) - 2
	if len(near) != want {
		t.Fatalf("%d roads within 50 m of %s, want %d (its neighbours)", len(near), r.ID, want)
	}
	far, _ := s.RoadsNear(ctx, id, []string{r.ID}, 2000)
	if len(far) <= len(near) {
		t.Fatal("a larger radius should find more roads")
	}
	st, err := s.StationsNear(ctx, id, "D1", 0)
	if err != nil || len(st) == 0 {
		t.Fatalf("district D1 should contain its own air station (%v)", err)
	}
}

func TestScenarioLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sc := &Scenario{ID: "sc-1", Name: "Close Highway A", Kind: "road_closure", Params: json.RawMessage(`{"road_ids":["R1"]}`),
		Seed: 42, BaseTick: 3600, DurationMin: 60, SpecHash: "abc"}
	if err := s.CreateScenario(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, "sc-1", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProgress(ctx, "sc-1", "running branches", 0.5); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, "sc-1", "hash1", []byte(`{"baseline":{"avg_trip_min":20},"scenario":{"avg_trip_min":22},"series":{}}`)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetScenario(ctx, "sc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.Progress != 1 || got.ResultHash == nil || *got.ResultHash != "hash1" || len(got.Result) == 0 {
		t.Fatalf("unexpected scenario %+v", got)
	}
	list, err := s.ListScenarios(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	var head struct {
		Scenario struct {
			AvgTripMin float64 `json:"avg_trip_min"`
		} `json:"scenario"`
	}
	if json.Unmarshal(list[0].Result, &head) != nil || head.Scenario.AvgTripMin != 22 {
		t.Fatalf("list should carry the summary: %s", list[0].Result)
	}
	if _, err := s.GetScenario(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing scenario: %v", err)
	}
	if err := s.MarkRunning(ctx, "sc-1", "worker-b"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a finished scenario must not be claimed again")
	}
}

func TestRecorderAndRetention(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	c := city.Generate(42)
	cityID, _ := s.EnsureCity(ctx, c)
	if err := s.EnsureRun(ctx, "run-1", cityID, 42, "live"); err != nil {
		t.Fatal(err)
	}
	e := sim.New(c)
	for day := 0; day < 3; day++ {
		e.StepN(86400)
		if err := s.RecordTelemetry(ctx, "run-1", e.Latest()); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	s.Pool.QueryRow(ctx, `SELECT count(*) FROM traffic_observations`).Scan(&n)
	if n != 3*len(c.Roads) {
		t.Fatalf("%d traffic rows, want %d", n, 3*len(c.Roads))
	}
	deleted, err := s.Cleanup(ctx, Retention{KeepSimDays: 1, KeepRuns: 3, KeepScenarios: 100})
	if err != nil {
		t.Fatal(err)
	}
	s.Pool.QueryRow(ctx, `SELECT count(*) FROM traffic_observations`).Scan(&n)
	if deleted == 0 || n != 2*len(c.Roads) {
		t.Fatalf("retention kept %d rows (deleted %d), want the last 1 day = %d", n, deleted, 2*len(c.Roads))
	}
}
