// Command sim is the City Simulation Worker: it runs the deterministic
// simulation on the shared clock and publishes to Redis Streams.
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
	"nexuscity/internal/simworker"
)

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	healthAddr := env("SIM_HEALTH_ADDR", ":8081")
	health.RunIfRequested(health.LocalURL(healthAddr, "/healthz"))

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := simworker.DefaultConfig()
	cfg.Version = version
	cfg.Hostname, _ = os.Hostname()
	var err error
	if cfg.Seed, err = strconv.ParseInt(env("SIM_SEED", "42"), 10, 64); err != nil {
		slog.Error("SIM_SEED must be an integer", "err", err)
		os.Exit(2)
	}
	if cfg.Speed, err = strconv.ParseFloat(env("SIM_SPEED", "10"), 64); err != nil {
		slog.Error("SIM_SPEED must be a number", "err", err)
		os.Exit(2)
	}
	cfg.Paused = env("SIM_START_PAUSED", "false") == "true"
	cfg.Resume = env("SIM_RESUME", "true") == "true"

	rdb, err := bus.Connect(ctx, env("REDIS_URL", "redis://localhost:6379/0"))
	if err != nil {
		slog.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	worker := simworker.New(cfg, rdb)

	// Health and metrics endpoint for container and Kubernetes probes.
	metrics.RegisterSim()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !worker.Healthy() {
			http.Error(w, "simulation loop stalled", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	hs := &http.Server{Addr: healthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health server", "err", err)
		}
	}()
	defer hs.Close()

	if err := worker.Run(ctx); err != nil {
		slog.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}
