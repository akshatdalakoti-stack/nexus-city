// Command scenario is the Scenario Execution Worker: it consumes the
// scenario queue (a Redis consumer group), runs what-if experiments and
// stores their results in PostgreSQL.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nexuscity/internal/bus"
	"nexuscity/internal/health"
	"nexuscity/internal/metrics"
	"nexuscity/internal/scenarioworker"
	"nexuscity/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	healthAddr := env("SCENARIO_HEALTH_ADDR", ":8082")
	health.RunIfRequested(health.LocalURL(healthAddr, "/healthz"))

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(2)
	}
	rdb, err := bus.Connect(ctx, env("REDIS_URL", "redis://localhost:6379/0"))
	if err != nil {
		slog.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()
	db, err := store.Open(ctx, dbURL)
	if err != nil {
		slog.Error("postgres", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	host, _ := os.Hostname()
	w := scenarioworker.New(scenarioworker.Config{
		Consumer:  env("SCENARIO_CONSUMER", host),
		ModelURLs: scenarioworker.ParseModelURLs(env("MODEL_URLS", "http://localhost:8090")),
	}, rdb, db)

	mux := http.NewServeMux()
	metrics.RegisterScenario()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	hs := &http.Server{Addr: healthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.ListenAndServe() }()
	defer hs.Close()

	if err := w.Run(ctx); err != nil {
		slog.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}
