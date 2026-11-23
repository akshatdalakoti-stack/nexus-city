// Command recorder is the Data Storage Layer writer: it persists the live
// run's 5-minute observations, events and forecasts to PostgreSQL and
// enforces data retention.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"nexuscity/internal/bus"
	"nexuscity/internal/health"
	"nexuscity/internal/metrics"
	"nexuscity/internal/recorder"
	"nexuscity/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func main() {
	healthAddr := env("RECORDER_HEALTH_ADDR", ":8083")
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
	rec := recorder.New(rdb, db, host, store.Retention{
		KeepSimDays:   envInt("RETENTION_SIM_DAYS", 7),
		KeepRuns:      envInt("RETENTION_RUNS", 3),
		KeepScenarios: envInt("RETENTION_SCENARIOS", 200),
	})
	mux := http.NewServeMux()
	metrics.RegisterRecorder()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	hs := &http.Server{Addr: healthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.ListenAndServe() }()
	defer hs.Close()
	slog.Info("recorder running")
	if err := rec.Run(ctx); err != nil {
		slog.Error("recorder stopped", "err", err)
		os.Exit(1)
	}
}
