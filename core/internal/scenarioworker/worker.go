// Package scenarioworker consumes the scenario queue: it claims a scenario,
// runs the baseline and scenario branches, adds spatial context and model
// forecasts, and stores the result. Several workers can share the queue
// through a Redis consumer group; Phase 5 scales them with KEDA.
package scenarioworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/bus"
	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/metrics"
	"nexuscity/internal/scenario"
	"nexuscity/internal/store"
)

// Config controls a worker.
type Config struct {
	Consumer   string            // unique name within the consumer group (the hostname)
	ModelURLs  map[string]string // model -> base URL of its inference service
	ClaimIdle  time.Duration     // reclaim scenarios a crashed worker left unacknowledged
	JobTimeout time.Duration     // upper bound for one scenario, including during shutdown
}

// ParseModelURLs reads "traffic=http://a:8090,energy=http://b:8090" or a
// single URL that serves every model.
func ParseModelURLs(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	if !strings.Contains(s, "=") {
		for _, m := range []string{"traffic", "energy", "pm25"} {
			out[m] = strings.TrimRight(s, "/")
		}
		return out
	}
	for _, part := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	return out
}

// Worker processes scenarios one at a time.
type Worker struct {
	cfg  Config
	rdb  *redis.Client
	db   *store.Store
	http *http.Client
}

// New creates a worker.
func New(cfg Config, rdb *redis.Client, db *store.Store) *Worker {
	if cfg.ClaimIdle == 0 {
		cfg.ClaimIdle = 10 * time.Minute
	}
	if cfg.JobTimeout == 0 {
		cfg.JobTimeout = 5 * time.Minute
	}
	return &Worker{cfg: cfg, rdb: rdb, db: db, http: &http.Client{}}
}

// EnsureGroup creates the consumer group (and the stream) if needed.
func EnsureGroup(ctx context.Context, rdb redis.Cmdable) error {
	err := rdb.XGroupCreateMkStream(ctx, events.StreamScenarios, events.GroupScenarioWorkers, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Run consumes the queue until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if err := EnsureGroup(ctx, w.rdb); err != nil {
		return err
	}
	slog.Info("scenario worker ready", "consumer", w.cfg.Consumer, "models", w.cfg.ModelURLs)
	for ctx.Err() == nil {
		msgs, err := w.next(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("queue read failed", "err", err)
				time.Sleep(2 * time.Second)
			}
			continue
		}
		// A scenario that has started is finished even if shutdown begins:
		// Kubernetes may scale this worker down while it is busy, and the
		// pod's grace period covers a run. Only new reads stop on ctx.
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.JobTimeout)
		for _, m := range msgs {
			id, _ := m.Values["id"].(string)
			if id != "" {
				w.Process(work, id)
			}
			w.rdb.XAck(work, events.StreamScenarios, events.GroupScenarioWorkers, m.ID)
		}
		cancel()
	}
	w.leaveGroup()
	return nil
}

// leaveGroup removes this worker from the consumer group on shutdown, so
// replicas that come and go (autoscaling) don't accumulate as dead
// consumers. It only leaves when it holds no unacknowledged scenarios,
// because deleting a consumer drops its pending entries.
func (w *Worker) leaveGroup() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p, err := w.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: events.StreamScenarios, Group: events.GroupScenarioWorkers,
		Consumer: w.cfg.Consumer, Start: "-", End: "+", Count: 1}).Result()
	if err != nil || len(p) > 0 {
		return
	}
	if err := w.rdb.XGroupDelConsumer(ctx, events.StreamScenarios, events.GroupScenarioWorkers, w.cfg.Consumer).Err(); err == nil {
		slog.Info("left the consumer group", "consumer", w.cfg.Consumer)
	}
}

// next returns a reclaimed stale message if any, otherwise waits for a new one.
func (w *Worker) next(ctx context.Context) ([]redis.XMessage, error) {
	claimed, _, err := w.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: events.StreamScenarios, Group: events.GroupScenarioWorkers, Consumer: w.cfg.Consumer,
		MinIdle: w.cfg.ClaimIdle, Start: "0-0", Count: 1,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(claimed) > 0 {
		slog.Info("reclaimed an abandoned scenario", "message", claimed[0].ID)
		return claimed, nil
	}
	res, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: events.GroupScenarioWorkers, Consumer: w.cfg.Consumer,
		Streams: []string{events.StreamScenarios, ">"}, Count: 1, Block: 5 * time.Second,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, nil
	}
	return res[0].Messages, nil
}

func (w *Worker) publish(ctx context.Context, sc *store.Scenario, u events.ScenarioUpdate) {
	payload, _ := json.Marshal(u)
	env := events.Envelope{Schema: events.SchemaScenario, Seed: sc.Seed, Tick: sc.BaseTick, EmittedAt: time.Now().UTC(), Payload: payload}
	if sc.BaseRunID != nil {
		env.RunID = *sc.BaseRunID
	}
	if _, err := bus.Publish(ctx, w.rdb, events.StreamScenarioUpdates, env); err != nil {
		slog.Warn("publish update failed", "err", err)
	}
}

// Process runs one scenario to completion (or failure).
func (w *Worker) Process(ctx context.Context, id string) {
	sc, err := w.db.GetScenario(ctx, id)
	if err != nil {
		slog.Error("scenario not loadable", "id", id, "err", err)
		return
	}
	if sc.Status == "done" || sc.Status == "failed" {
		return // already handled (e.g. a duplicate queue entry)
	}
	if err := w.db.MarkRunning(ctx, id, w.cfg.Consumer); err != nil {
		slog.Error("could not claim scenario", "id", id, "err", err)
		return
	}
	w.publish(ctx, sc, events.ScenarioUpdate{ID: id, Status: "running", Phase: "starting", Worker: w.cfg.Consumer})
	started := time.Now()
	metrics.ScenarioBusy.Set(1)
	status := "failed"
	defer func() {
		metrics.ScenarioBusy.Set(0)
		metrics.ScenarioJobs.WithLabelValues(sc.Kind, status).Inc()
		metrics.ScenarioSeconds.WithLabelValues(sc.Kind).Observe(time.Since(started).Seconds())
	}()
	slog.Info("running scenario", "id", id, "kind", sc.Kind, "base_tick", sc.BaseTick, "duration_min", sc.DurationMin)

	fail := func(err error) {
		slog.Error("scenario failed", "id", id, "err", err)
		_ = w.db.Fail(ctx, id, err.Error())
		w.publish(ctx, sc, events.ScenarioUpdate{ID: id, Status: "failed", Error: err.Error(), Worker: w.cfg.Consumer})
	}
	var params scenario.Params
	if err := json.Unmarshal(sc.Params, &params); err != nil {
		fail(fmt.Errorf("params: %w", err))
		return
	}
	spec := scenario.Spec{Kind: sc.Kind, Seed: sc.Seed, BaseTick: sc.BaseTick, DurationMin: sc.DurationMin, Params: params}

	last := time.Time{}
	progress := func(phase string, f float64) {
		if time.Since(last) < 500*time.Millisecond && f < 1 {
			return
		}
		last = time.Now()
		_ = w.db.SetProgress(ctx, id, phase, f*0.9)
		w.publish(ctx, sc, events.ScenarioUpdate{ID: id, Status: "running", Phase: phase, Progress: f * 0.9, Worker: w.cfg.Consumer})
	}
	res, branches, err := scenario.Run(spec, progress)
	if err != nil {
		fail(err)
		return
	}
	c := city.Generate(spec.Seed)

	progress("adding spatial context", 0.92)
	if ctxJSON, err := w.spatialContext(ctx, c, spec, res); err == nil {
		res.Context = ctxJSON
	} else {
		slog.Warn("spatial context unavailable", "err", err)
	}
	progress("asking the models", 0.95)
	fc := w.compareForecasts(ctx, c, branches)
	res.Forecasts, _ = json.Marshal(fc)

	raw, err := json.Marshal(res)
	if err != nil {
		fail(err)
		return
	}
	if err := w.db.Finish(ctx, id, res.ResultHash, raw); err != nil {
		fail(err)
		return
	}
	status = "done"
	w.publish(ctx, sc, events.ScenarioUpdate{ID: id, Status: "done", Progress: 1, ResultHash: res.ResultHash, Worker: w.cfg.Consumer})
	slog.Info("scenario done", "id", id, "result_hash", res.ResultHash, "run_s", res.RunS)
}

// spatialContext uses PostGIS to describe what is near the change: roads
// within 1.5 km of a closure (with their congestion change) or air
// stations within 3 km of an industrial district (with PM2.5 change).
func (w *Worker) spatialContext(ctx context.Context, c *city.City, spec scenario.Spec, res *scenario.Result) (json.RawMessage, error) {
	cityID, err := w.db.EnsureCity(ctx, c)
	if err != nil {
		return nil, err
	}
	type near struct {
		ID       string  `json:"id"`
		Distance float64 `json:"distance_m"`
		Delta    float64 `json:"delta"`
	}
	out := map[string]any{}
	switch spec.Kind {
	case scenario.RoadClosure:
		roads, err := w.db.RoadsNear(ctx, cityID, spec.Params.RoadIDs, 1500)
		if err != nil {
			return nil, err
		}
		delta := map[string]float64{}
		for _, r := range res.Roads {
			delta[r.ID] = r.Delta
		}
		list := make([]near, 0, len(roads))
		var sum float64
		for _, r := range roads {
			list = append(list, near{r.ID, r.Distance, delta[r.ID]})
			sum += delta[r.ID]
		}
		out["radius_m"] = 1500
		out["nearby_roads"] = list
		if len(list) > 0 {
			out["nearby_mean_delta"] = sum / float64(len(list))
		}
	case scenario.IndustrialSpike:
		st, err := w.db.StationsNear(ctx, cityID, spec.Params.DistrictID, 3000)
		if err != nil {
			return nil, err
		}
		out["radius_m"] = 3000
		out["nearby_stations"] = st
	default:
		return nil, nil
	}
	return json.Marshal(out)
}
