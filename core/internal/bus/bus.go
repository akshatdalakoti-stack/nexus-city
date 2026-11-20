// Package bus wraps the Redis Streams calls shared by the simulation worker
// and the gateway.
package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/events"
	"nexuscity/internal/metrics"
)

// Stream lengths kept in Redis. Trimming is approximate (~), which is much
// cheaper for Redis. Telemetry keeps one simulated day (~32 KB per minute).
var MaxLen = map[string]int64{
	events.StreamFrames:    50,
	events.StreamTelemetry: 1440,
	events.StreamEvents:    5000,
	events.StreamClock:     200,
	events.StreamControl:   200,
	events.StreamForecasts: 3000,
	events.StreamAnomalies: 1000,
}

// Connect parses a redis:// URL and waits until Redis answers, retrying
// with backoff so services can start before Redis is ready.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	delay := 500 * time.Millisecond
	for {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := rdb.Ping(pctx).Err()
		cancel()
		if err == nil {
			return rdb, nil
		}
		slog.Warn("waiting for redis", "url", opts.Addr, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// Publish appends an envelope to a stream.
func Publish(ctx context.Context, rdb redis.Cmdable, stream string, env events.Envelope) (string, error) {
	data, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	id, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: MaxLen[stream],
		Approx: true,
		Values: map[string]any{"data": data},
	}).Result()
	if err != nil {
		metrics.PublishErrors.WithLabelValues(stream).Inc()
	} else {
		metrics.Published.WithLabelValues(stream).Inc()
	}
	return id, err
}

// Data extracts the JSON payload from a stream message.
func Data(m redis.XMessage) ([]byte, bool) {
	switch v := m.Values["data"].(type) {
	case string:
		return []byte(v), true
	case []byte:
		return v, true
	}
	return nil, false
}

// LastID returns the newest entry id of a stream, or "0-0" if it is empty,
// so a reader can start strictly after what already exists.
func LastID(ctx context.Context, rdb redis.Cmdable, stream string) string {
	msgs, err := rdb.XRevRangeN(ctx, stream, "+", "-", 1).Result()
	if err != nil || len(msgs) == 0 {
		return "0-0"
	}
	return msgs[0].ID
}
