// Package recorder persists the live run to PostgreSQL: 5-minute
// observations, every event and every model forecast. It reads the streams
// through its own consumer group, so after downtime it catches up on
// whatever is still in the streams, and it applies the retention policy.
package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/bus"
	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/metrics"
	"nexuscity/internal/store"
)

const Group = "recorder"

var streams = []string{events.StreamTelemetry, events.StreamEvents, events.StreamForecasts}

// Recorder copies stream data into the database.
type Recorder struct {
	rdb       *redis.Client
	db        *store.Store
	consumer  string
	retention store.Retention
	runs      map[string]bool // runs already registered
	Written   map[string]int  // rows written per stream (for tests and logs)
}

// New creates a recorder.
func New(rdb *redis.Client, db *store.Store, consumer string, r store.Retention) *Recorder {
	return &Recorder{rdb: rdb, db: db, consumer: consumer, retention: r, runs: map[string]bool{}, Written: map[string]int{}}
}

// Run consumes until ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) error {
	for _, st := range streams {
		if err := r.rdb.XGroupCreateMkStream(ctx, st, Group, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
			return err
		}
	}
	lastCleanup := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastCleanup) > 10*time.Minute {
			if n, err := r.db.Cleanup(ctx, r.retention); err != nil {
				slog.Warn("retention cleanup failed", "err", err)
			} else if n > 0 {
				slog.Info("retention cleanup", "rows_deleted", n)
			}
			lastCleanup = time.Now()
		}
		if err := r.Poll(ctx, 5*time.Second); err != nil && ctx.Err() == nil {
			slog.Warn("recorder read failed", "err", err)
			time.Sleep(2 * time.Second)
		}
	}
	return nil
}

// Poll reads and stores one batch from every stream.
func (r *Recorder) Poll(ctx context.Context, block time.Duration) error {
	args := append(slices.Clone(streams), ">", ">", ">")
	res, err := r.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: r.consumer, Streams: args, Count: 200, Block: block}).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, st := range res {
		for _, m := range st.Messages {
			if data, ok := bus.Data(m); ok {
				if err := r.store(ctx, st.Stream, data); err != nil && !isDuplicate(err) {
					metrics.RecorderErrors.Inc()
					slog.Warn("could not record", "stream", st.Stream, "err", err)
				}
			}
			r.rdb.XAck(ctx, st.Stream, Group, m.ID)
		}
	}
	return nil
}

func isDuplicate(err error) bool { return strings.Contains(err.Error(), "duplicate key") }

func (r *Recorder) ensureRun(ctx context.Context, env events.Envelope) error {
	if r.runs[env.RunID] {
		return nil
	}
	cityID, err := r.db.EnsureCity(ctx, city.Generate(env.Seed))
	if err != nil {
		return err
	}
	if err := r.db.EnsureRun(ctx, env.RunID, cityID, env.Seed, "live"); err != nil {
		return err
	}
	r.runs[env.RunID] = true
	return nil
}

func (r *Recorder) store(ctx context.Context, stream string, data []byte) error {
	var env events.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	if env.RunID == "" {
		return nil
	}
	if err := r.ensureRun(ctx, env); err != nil {
		return err
	}
	switch stream {
	case events.StreamTelemetry:
		if env.Tick%300 != 0 {
			return nil // observations are kept at 5-minute resolution
		}
		var t events.Telemetry
		if err := json.Unmarshal(env.Payload, &t); err != nil {
			return err
		}
		if err := r.db.RecordTelemetry(ctx, env.RunID, &t); err != nil {
			return err
		}
	case events.StreamEvents:
		var ev events.Event
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			return err
		}
		if err := r.db.RecordEvent(ctx, env.RunID, env.Tick, &ev); err != nil {
			return err
		}
	case events.StreamForecasts:
		var f events.Forecast
		if err := json.Unmarshal(env.Payload, &f); err != nil {
			return err
		}
		if err := r.db.RecordForecast(ctx, env.RunID, &f); err != nil {
			return err
		}
	}
	r.Written[stream]++
	metrics.RecorderWritten.WithLabelValues(stream).Inc()
	return nil
}
