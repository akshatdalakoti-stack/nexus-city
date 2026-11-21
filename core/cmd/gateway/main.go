// Command gateway is the API Gateway: REST + WebSocket access to the live
// simulation.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"nexuscity/internal/bus"
	"nexuscity/internal/gateway"
	"nexuscity/internal/health"
	"nexuscity/internal/metrics"
	"nexuscity/internal/store"
)

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	health.RunIfRequested(health.LocalURL(env("GATEWAY_ADDR", ":8080"), "/api/health"))

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rdb, err := bus.Connect(ctx, env("REDIS_URL", "redis://localhost:6379/0"))
	if err != nil {
		slog.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	hist, err := strconv.Atoi(env("GATEWAY_HISTORY_LIMIT", "1440"))
	if err != nil {
		slog.Error("GATEWAY_HISTORY_LIMIT must be an integer", "err", err)
		os.Exit(2)
	}
	var origins []string
	for _, o := range strings.Split(env("ALLOWED_ORIGINS", "http://localhost:3000"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	cfg := gateway.Config{AllowedOrigins: origins, HistoryLimit: hist, Version: version,
		AssistantURL: os.Getenv("ASSISTANT_URL"), PrometheusURL: os.Getenv("PROMETHEUS_URL"),
		GrafanaURL: os.Getenv("GRAFANA_PUBLIC_URL")}
	if url := os.Getenv("DATABASE_URL"); url != "" {
		db, err := store.Open(ctx, url)
		if err != nil {
			slog.Error("postgres", "err", err)
			os.Exit(1)
		}
		defer db.Close()
		cfg.Store = db
	} else {
		slog.Warn("DATABASE_URL not set: scenarios are disabled")
	}
	srv := gateway.New(cfg, rdb)
	metrics.RegisterGateway(srv.QueueCollector())
	go srv.Consume(ctx)

	addr := env("GATEWAY_ADDR", ":8080")
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	slog.Info("gateway listening", "addr", addr, "origins", origins)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("http server", "err", err)
		os.Exit(1)
	}
}
