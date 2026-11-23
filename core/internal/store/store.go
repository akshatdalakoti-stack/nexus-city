// Package store is the PostgreSQL + PostGIS data layer: versioned
// migrations, city geometry, scenario configs and results, and the
// recorder's observation, event and forecast tables.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nexuscity/internal/city"
	"nexuscity/internal/events"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps a connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Open connects, retrying until the database answers, and applies migrations.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 6
	delay := 500 * time.Millisecond
	for {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = pool.Ping(pctx)
			cancel()
			if err == nil {
				s := &Store{Pool: pool}
				if err := s.Migrate(ctx); err != nil {
					pool.Close()
					return nil, err
				}
				return s, nil
			}
			pool.Close()
		}
		slog.Warn("waiting for postgres", "host", cfg.ConnConfig.Host, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// Close releases the pool.
func (s *Store) Close() { s.Pool.Close() }

// Migrate applies pending migrations in order. An advisory lock keeps
// several services that start together from migrating at the same time.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7413)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7413)`) //nolint:errcheck
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(strings.TrimPrefix(f, "migrations/"), ".sql")
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("applied migration", "version", version)
	}
	return nil
}

// ---------------------------------------------------------------- city

func wktPoint(lon, lat float64) string { return fmt.Sprintf("POINT(%f %f)", lon, lat) }

func wktLine(coords [][2]float64) string {
	parts := make([]string, len(coords))
	for i, c := range coords {
		parts[i] = fmt.Sprintf("%f %f", c[0], c[1])
	}
	return "LINESTRING(" + strings.Join(parts, ", ") + ")"
}

func wktPolygon(ring [][2]float64) string {
	parts := make([]string, len(ring))
	for i, c := range ring {
		parts[i] = fmt.Sprintf("%f %f", c[0], c[1])
	}
	return "POLYGON((" + strings.Join(parts, ", ") + "))"
}

// EnsureCity stores a city's geometry once and returns its id.
func (s *Store) EnsureCity(ctx context.Context, c *city.City) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `SELECT id FROM cities WHERE seed = $1 AND generator_version = $2`, c.Seed, c.Version).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	err = tx.QueryRow(ctx, `INSERT INTO cities (seed, generator_version, name) VALUES ($1, $2, $3)
		ON CONFLICT (seed, generator_version) DO UPDATE SET name = EXCLUDED.name RETURNING id`, c.Seed, c.Version, c.Name).Scan(&id)
	if err != nil {
		return 0, err
	}
	b := &pgx.Batch{}
	for _, d := range c.Districts {
		b.Queue(`INSERT INTO districts VALUES ($1,$2,$3,$4,$5,$6,$7,$8, ST_GeomFromText($9, 4326)) ON CONFLICT DO NOTHING`,
			id, d.ID, d.Name, d.Type, d.Population, d.Jobs, d.BaseLoadMW, d.SubstationMW, wktPolygon(d.Polygon))
	}
	for _, n := range c.Nodes {
		b.Queue(`INSERT INTO intersections VALUES ($1,$2,$3,$4,$5,$6,$7, ST_GeomFromText($8, 4326)) ON CONFLICT DO NOTHING`,
			id, n.ID, n.Name, n.Signalized, nullInt(n.CycleS), nullInt(n.GreenNSS), nullInt(n.OffsetS), wktPoint(n.Lon, n.Lat))
	}
	for _, r := range c.Roads {
		b.Queue(`INSERT INTO road_segments VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, ST_GeomFromText($10, 4326)) ON CONFLICT DO NOTHING`,
			id, r.ID, r.Name, r.Segment, r.Class, r.Lanes, r.SpeedKmh, r.LengthM, c.Districts[r.District].ID, wktLine(r.Coords))
	}
	for _, f := range c.Facilities {
		b.Queue(`INSERT INTO facilities VALUES ($1,$2,$3,$4,$5,$6, ST_GeomFromText($7, 4326)) ON CONFLICT DO NOTHING`,
			id, f.ID, f.Kind, f.Name, c.Districts[f.District].ID, nullInt(f.Capacity), wktPoint(f.Lon, f.Lat))
	}
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return 0, fmt.Errorf("city geometry: %w", err)
	}
	return id, tx.Commit(ctx)
}

func nullInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// EnsureRun records a simulation run.
func (s *Store) EnsureRun(ctx context.Context, runID string, cityID, seed int64, kind string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO simulation_runs (run_id, city_id, seed, kind) VALUES ($1, $2, $3, $4)
		ON CONFLICT (run_id) DO NOTHING`, runID, cityID, seed, kind)
	return err
}

// RoadsNear returns roads within `meters` of any of the given roads,
// nearest first (PostGIS, measured on the spheroid).
func (s *Store) RoadsNear(ctx context.Context, cityID int64, roadIDs []string, meters float64) ([]NearRoad, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT r.id, MIN(ST_Distance(r.geom::geography, c.geom::geography)) AS d
		FROM road_segments r
		JOIN road_segments c ON c.city_id = r.city_id AND c.id = ANY($2)
		WHERE r.city_id = $1 AND NOT r.id = ANY($2)
		  AND ST_DWithin(r.geom::geography, c.geom::geography, $3)
		GROUP BY r.id ORDER BY d`, cityID, roadIDs, meters)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[NearRoad])
}

// NearRoad is a road and its distance from a reference geometry.
type NearRoad struct {
	ID       string  `json:"id"`
	Distance float64 `json:"distance_m"`
}

// StationsNear returns air-quality stations within `meters` of a district.
func (s *Store) StationsNear(ctx context.Context, cityID int64, districtID string, meters float64) ([]NearRoad, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.id, ST_Distance(f.geom::geography, d.geom::geography) AS dist
		FROM facilities f JOIN districts d ON d.city_id = f.city_id AND d.id = $2
		WHERE f.city_id = $1 AND f.kind = 'aq_station' AND ST_DWithin(f.geom::geography, d.geom::geography, $3)
		ORDER BY dist`, cityID, districtID, meters)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[NearRoad])
}

// ------------------------------------------------------------ scenarios

// Scenario is a scenario_configs row plus its result when finished.
type Scenario struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Params      json.RawMessage `json:"params"`
	Seed        int64           `json:"seed"`
	BaseRunID   *string         `json:"base_run_id"`
	BaseTick    int64           `json:"base_tick"`
	DurationMin int             `json:"duration_min"`
	SpecHash    string          `json:"spec_hash"`
	Status      string          `json:"status"`
	Progress    float64         `json:"progress"`
	Phase       *string         `json:"phase"`
	Error       *string         `json:"error"`
	Worker      *string         `json:"worker"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
	ResultHash  *string         `json:"result_hash,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

const scenarioCols = `c.id, c.name, c.kind, c.params, c.seed, c.base_run_id, c.base_tick, c.duration_min, c.spec_hash,
	c.status, c.progress, c.phase, c.error, c.worker, c.created_at, c.started_at, c.finished_at`

func scanScenario(row pgx.Row, withResult bool) (*Scenario, error) {
	var sc Scenario
	dest := []any{&sc.ID, &sc.Name, &sc.Kind, &sc.Params, &sc.Seed, &sc.BaseRunID, &sc.BaseTick, &sc.DurationMin, &sc.SpecHash,
		&sc.Status, &sc.Progress, &sc.Phase, &sc.Error, &sc.Worker, &sc.CreatedAt, &sc.StartedAt, &sc.FinishedAt}
	if withResult {
		dest = append(dest, &sc.ResultHash, &sc.Result)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sc, nil
}

// CreateScenario inserts a queued scenario.
func (s *Store) CreateScenario(ctx context.Context, sc *Scenario) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO scenario_configs (id, name, kind, params, seed, base_run_id, base_tick, duration_min, spec_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, sc.ID, sc.Name, sc.Kind, sc.Params, sc.Seed, sc.BaseRunID, sc.BaseTick, sc.DurationMin, sc.SpecHash)
	return err
}

// GetScenario returns a scenario with its result (if finished).
func (s *Store) GetScenario(ctx context.Context, id string) (*Scenario, error) {
	return scanScenario(s.Pool.QueryRow(ctx, `SELECT `+scenarioCols+`, r.result_hash, r.result
		FROM scenario_configs c LEFT JOIN scenario_results r ON r.scenario_id = c.id WHERE c.id = $1`, id), true)
}

// ListScenarios returns the most recent scenarios, without full results
// but with each branch's headline summary.
func (s *Store) ListScenarios(ctx context.Context, limit int) ([]*Scenario, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scenarioCols+`, r.result_hash,
			CASE WHEN r.result IS NULL THEN NULL ELSE jsonb_build_object('baseline', r.result->'baseline', 'scenario', r.result->'scenario') END
		FROM scenario_configs c LEFT JOIN scenario_results r ON r.scenario_id = c.id
		ORDER BY c.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Scenario{}
	for rows.Next() {
		sc, err := scanScenario(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// MarkRunning claims a scenario for a worker.
func (s *Store) MarkRunning(ctx context.Context, id, worker string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE scenario_configs SET status = 'running', worker = $2, started_at = now(), progress = 0, error = NULL
		WHERE id = $1 AND status IN ('queued', 'running')`, id, worker)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetProgress records how far a running scenario has got.
func (s *Store) SetProgress(ctx context.Context, id, phase string, progress float64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE scenario_configs SET phase = $2, progress = $3 WHERE id = $1`, id, phase, progress)
	return err
}

// Finish stores the result and marks the scenario done.
func (s *Store) Finish(ctx context.Context, id, resultHash string, result []byte) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `INSERT INTO scenario_results (scenario_id, result_hash, result) VALUES ($1, $2, $3)
		ON CONFLICT (scenario_id) DO UPDATE SET result_hash = EXCLUDED.result_hash, result = EXCLUDED.result, created_at = now()`,
		id, resultHash, result); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE scenario_configs SET status = 'done', progress = 1, phase = 'done', finished_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fail marks a scenario failed.
func (s *Store) Fail(ctx context.Context, id, msg string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE scenario_configs SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`, id, msg)
	return err
}

// --------------------------------------------------------- recorder

// RecordTelemetry writes one 5-minute observation of the live run.
func (s *Store) RecordTelemetry(ctx context.Context, runID string, t *events.Telemetry) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ts := t.SimTime
	roads := make([][]any, len(t.Roads))
	for i, o := range t.Roads {
		var inc *string
		if o.Incident != "" {
			v := o.Incident
			inc = &v
		}
		roads[i] = []any{runID, t.Tick, ts, o.ID, float32(o.FlowVph), float32(o.SpeedKmh), float32(o.Congestion), o.Vehicles, inc}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"traffic_observations"},
		[]string{"run_id", "tick", "sim_time", "road_id", "flow_vph", "speed_kmh", "congestion", "vehicles", "incident"},
		pgx.CopyFromRows(roads)); err != nil {
		return fmt.Errorf("traffic: %w", err)
	}
	dist := make([][]any, len(t.Districts))
	for i, o := range t.Districts {
		dist[i] = []any{runID, t.Tick, ts, o.ID, float32(o.DemandMW), float32(o.Utilization), float32(t.Weather.TempC)}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"electricity_observations"},
		[]string{"run_id", "tick", "sim_time", "district_id", "demand_mw", "utilization", "temp_c"}, pgx.CopyFromRows(dist)); err != nil {
		return fmt.Errorf("electricity: %w", err)
	}
	sts := make([][]any, len(t.Stations))
	for i, o := range t.Stations {
		var pm *float32
		if o.PM25 != nil {
			v := float32(*o.PM25)
			pm = &v
		}
		sts[i] = []any{runID, t.Tick, ts, o.ID, pm, o.Status}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"pollution_observations"},
		[]string{"run_id", "tick", "sim_time", "station_id", "pm25", "status"}, pgx.CopyFromRows(sts)); err != nil {
		return fmt.Errorf("pollution: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE simulation_runs SET last_tick = GREATEST(last_tick, $2) WHERE run_id = $1`, runID, t.Tick); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordEvent stores one simulation event.
func (s *Store) RecordEvent(ctx context.Context, runID string, tick int64, ev *events.Event) error {
	data, _ := json.Marshal(ev.Data)
	_, err := s.Pool.Exec(ctx, `INSERT INTO simulation_events (run_id, tick, sim_time, kind, severity, entity_type, entity_id, message, data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, runID, tick, ev.SimTime, ev.Kind, ev.Severity, ev.EntityType, ev.EntityID, ev.Message, data)
	return err
}

// RecordForecast stores a model's forecast and registers its version.
func (s *Store) RecordForecast(ctx context.Context, runID string, f *events.Forecast) error {
	if _, err := s.Pool.Exec(ctx, `INSERT INTO model_versions (version, kind) VALUES ($1, $2)
		ON CONFLICT (version) DO UPDATE SET last_seen = now()`, f.Version, f.Model); err != nil {
		return err
	}
	var rows [][]any
	for hi, h := range f.Horizons {
		vals := f.Values[fmt.Sprint(h)]
		for i, id := range f.Entities {
			if i < len(vals) {
				rows = append(rows, []any{runID, f.Version, f.IssuedTick, f.TargetTicks[hi], h, f.EntityType, id, float32(vals[i])})
			}
		}
	}
	_, err := s.Pool.CopyFrom(ctx, pgx.Identifier{"forecasts"},
		[]string{"run_id", "model_version", "issued_tick", "target_tick", "horizon", "entity_type", "entity_id", "value"},
		pgx.CopyFromRows(rows))
	return err
}

// Retention bounds storage: observations and forecasts older than
// keepSimDays of their run are deleted, only the newest keepRuns live runs
// are kept, and finished scenarios beyond the newest keepScenarios go.
type Retention struct {
	KeepSimDays   int
	KeepRuns      int
	KeepScenarios int
}

// Cleanup applies the retention policy and reports how many rows went.
func (s *Store) Cleanup(ctx context.Context, r Retention) (int64, error) {
	var total int64
	stmts := []string{
		`DELETE FROM simulation_runs WHERE kind = 'live' AND run_id NOT IN
			(SELECT run_id FROM simulation_runs WHERE kind = 'live' ORDER BY started_at DESC LIMIT $1)`,
		`DELETE FROM traffic_observations o USING simulation_runs r WHERE o.run_id = r.run_id AND o.tick < r.last_tick - $1 * 86400`,
		`DELETE FROM electricity_observations o USING simulation_runs r WHERE o.run_id = r.run_id AND o.tick < r.last_tick - $1 * 86400`,
		`DELETE FROM pollution_observations o USING simulation_runs r WHERE o.run_id = r.run_id AND o.tick < r.last_tick - $1 * 86400`,
		`DELETE FROM forecasts o USING simulation_runs r WHERE o.run_id = r.run_id AND o.issued_tick < r.last_tick - $1 * 86400`,
		`DELETE FROM simulation_events o USING simulation_runs r WHERE o.run_id = r.run_id AND o.tick < r.last_tick - $1 * 86400`,
		`DELETE FROM scenario_configs WHERE status IN ('done', 'failed') AND id NOT IN
			(SELECT id FROM scenario_configs ORDER BY created_at DESC LIMIT $1)`,
	}
	args := []int{r.KeepRuns, r.KeepSimDays, r.KeepSimDays, r.KeepSimDays, r.KeepSimDays, r.KeepSimDays, r.KeepScenarios}
	for i, q := range stmts {
		tag, err := s.Pool.Exec(ctx, q, args[i])
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
