// Package gateway is the API Gateway: it consumes the simulation streams,
// keeps an in-memory view of the current run, serves REST endpoints and
// fans live updates out over WebSockets. It never computes city values
// itself; everything it serves comes from the simulation worker.
package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/bus"
	"nexuscity/internal/events"
	"nexuscity/internal/metrics"
	"nexuscity/internal/store"
)

// Config controls the gateway.
type Config struct {
	AllowedOrigins []string // e.g. http://localhost:3000; empty or "*" allows all
	HistoryLimit   int      // telemetry snapshots kept in memory
	Version        string
	Store          *store.Store // optional; scenarios need it
	AssistantURL   string       // optional City Intelligence service, e.g. http://localhost:8095
	PrometheusURL  string       // optional, for per-pod usage and the Infrastructure charts
	GrafanaURL     string       // optional public Grafana link shown on the Infrastructure page
}

// Server is the gateway.
type Server struct {
	cfg     Config
	rdb     *redis.Client
	hub     *Hub
	state   *State
	ml      *mlState
	infra   *infra
	started time.Time
	ready   chanFlag
}

type chanFlag struct{ ch chan struct{} }

func (f chanFlag) set() {
	select {
	case <-f.ch:
	default:
		close(f.ch)
	}
}

func (f chanFlag) isSet() bool {
	select {
	case <-f.ch:
		return true
	default:
		return false
	}
}

// New creates a gateway server.
func New(cfg Config, rdb *redis.Client) *Server {
	if cfg.HistoryLimit <= 0 {
		cfg.HistoryLimit = 1440
	}
	var hosts []string
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			hosts = nil
			break
		}
		hosts = append(hosts, strings.TrimPrefix(strings.TrimPrefix(o, "http://"), "https://"))
	}
	return &Server{cfg: cfg, rdb: rdb, hub: NewHub(hosts), state: NewState(cfg.HistoryLimit), ml: newMLState(),
		infra:   newInfra(cfg.PrometheusURL, cfg.GrafanaURL),
		started: time.Now(), ready: chanFlag{ch: make(chan struct{})}}
}

// State exposes the in-memory state (for tests).
func (s *Server) State() *State { return s.state }

// Hub exposes the WebSocket hub (for tests).
func (s *Server) Hub() *Hub { return s.hub }

// ---------------------------------------------------------------- consumer

// Consume reads the simulation streams until ctx is cancelled, restarting
// after Redis errors.
func (s *Server) Consume(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.consume(ctx); err != nil && ctx.Err() == nil {
			slog.Error("stream consumer failed; retrying", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (s *Server) consume(ctx context.Context) error {
	if err := s.bootstrap(ctx); err != nil {
		return err
	}
	streams := []string{events.StreamClock, events.StreamTelemetry, events.StreamEvents, events.StreamFrames,
		events.StreamForecasts, events.StreamAnomalies, events.StreamScenarioUpdates}
	ids := make([]string, len(streams))
	for i, st := range streams {
		ids[i] = bus.LastID(ctx, s.rdb, st)
	}
	s.ready.set()
	for ctx.Err() == nil {
		res, err := s.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: append(slices.Clone(streams), ids...), Block: 5 * time.Second, Count: 500,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return err
		}
		for _, st := range res {
			i := slices.Index(streams, st.Stream)
			for _, m := range st.Messages {
				ids[i] = m.ID
				if data, ok := bus.Data(m); ok {
					s.handle(ctx, st.Stream, data)
				}
			}
		}
	}
	return nil
}

// bootstrap loads the current run's city, clock, recent telemetry and
// events so the gateway can serve history right after a restart.
func (s *Server) bootstrap(ctx context.Context) error {
	clockRaw, err := s.rdb.Get(ctx, events.KeyClock).Bytes()
	if errors.Is(err, redis.Nil) {
		slog.Info("no simulation run yet; waiting for the worker")
		return nil
	}
	if err != nil {
		return err
	}
	var clk events.Envelope
	if err := json.Unmarshal(clockRaw, &clk); err != nil {
		return fmt.Errorf("clock: %w", err)
	}
	city, err := s.rdb.Get(ctx, events.KeyCity).Bytes()
	if err != nil {
		return fmt.Errorf("city: %w", err)
	}
	s.state.beginRun(clk.RunID, time.Time{}, city)
	s.state.setClock(clockRaw)

	msgs, err := s.rdb.XRevRangeN(ctx, events.StreamTelemetry, "+", "-", int64(s.cfg.HistoryLimit)).Result()
	if err != nil {
		return err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if data, ok := bus.Data(msgs[i]); ok {
			var env events.Envelope
			if json.Unmarshal(data, &env) == nil && env.RunID == clk.RunID {
				_, _ = s.state.addTelemetry(env, data)
			}
		}
	}
	evs, err := s.rdb.XRevRangeN(ctx, events.StreamEvents, "+", "-", 300).Result()
	if err != nil {
		return err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if data, ok := bus.Data(evs[i]); ok {
			s.state.addEvent(data)
		}
	}
	s.ml.reset()
	fcs, err := s.rdb.XRevRangeN(ctx, events.StreamForecasts, "+", "-", 400).Result()
	if err != nil {
		return err
	}
	for i := len(fcs) - 1; i >= 0; i-- {
		if data, ok := bus.Data(fcs[i]); ok {
			var env events.Envelope
			if json.Unmarshal(data, &env) == nil && env.RunID == clk.RunID {
				_ = s.ml.addForecast(env, data)
			}
		}
	}
	if an, err := s.rdb.XRevRangeN(ctx, events.StreamAnomalies, "+", "-", 1).Result(); err == nil && len(an) == 1 {
		if data, ok := bus.Data(an[0]); ok {
			var env events.Envelope
			if json.Unmarshal(data, &env) == nil && env.RunID == clk.RunID {
				s.ml.setAnomalies(data)
			}
		}
	}
	slog.Info("gateway bootstrapped", "run_id", clk.RunID, "telemetry", s.state.Range().Count)
	return nil
}

func (s *Server) handle(ctx context.Context, stream string, data []byte) {
	metrics.Consumed.WithLabelValues(stream).Inc()
	var env events.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		slog.Warn("bad envelope", "stream", stream, "err", err)
		return
	}
	// Scenario updates may refer to an earlier run; they never signal a new live run.
	if stream == events.StreamScenarioUpdates {
		s.hub.Broadcast(wrap("scenario", data))
		return
	}
	if s.state.stale(env) {
		return
	}
	if !s.state.isCurrent(env.RunID) {
		city, err := s.rdb.Get(ctx, events.KeyCity).Bytes()
		if err != nil {
			slog.Error("new run but no city", "err", err)
			return
		}
		s.state.beginRun(env.RunID, env.EmittedAt, city)
		s.ml.reset()
		slog.Info("new simulation run", "run_id", env.RunID)
		msg, _ := json.Marshal(map[string]string{"run_id": env.RunID})
		s.hub.Broadcast(wrap("run", msg))
	}
	switch stream {
	case events.StreamClock:
		s.state.setClock(data)
		s.hub.Broadcast(wrap("clock", data))
	case events.StreamTelemetry:
		tel, err := s.state.addTelemetry(env, data)
		if err != nil {
			slog.Warn("bad telemetry", "err", err)
			return
		}
		if tel != nil {
			s.ml.score(tel, s.state.TelemetryByTick)
		}
		s.hub.Broadcast(wrap("telemetry", data))
	case events.StreamEvents:
		s.state.addEvent(data)
		s.hub.Broadcast(wrap("event", data))
	case events.StreamForecasts:
		if err := s.ml.addForecast(env, data); err != nil {
			slog.Warn("bad forecast", "err", err)
			return
		}
		s.hub.Broadcast(wrap("forecast", data))
	case events.StreamAnomalies:
		s.ml.setAnomalies(data)
		s.hub.Broadcast(wrap("anomaly", data))
	case events.StreamFrames:
		s.state.setFrame(data)
		s.hub.Broadcast(wrap("frame", data))
	}
}

// ---------------------------------------------------------------- HTTP

// Handler returns the HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/ready", s.handleReady)
	mux.HandleFunc("GET /api/city", s.handleCity)
	mux.HandleFunc("GET /api/clock", s.handleGetClock)
	mux.HandleFunc("POST /api/clock", s.handlePostClock)
	mux.HandleFunc("POST /api/sim/reset", s.handleReset)
	mux.HandleFunc("GET /api/telemetry/latest", s.handleLatest)
	mux.HandleFunc("GET /api/telemetry/range", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.state.Range())
	})
	mux.HandleFunc("GET /api/telemetry", s.handleTelemetryAt)
	mux.HandleFunc("GET /api/history/{kind}/{id}", s.handleHistory)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/runtime", s.handleRuntime)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/models/{kind}", s.handleModelMeta)
	mux.HandleFunc("GET /api/forecasts/latest", s.handleForecastsLatest)
	mux.HandleFunc("GET /api/forecasts/{model}/{id}", s.handleEntityForecasts)
	mux.HandleFunc("GET /api/anomalies/latest", s.handleAnomaliesLatest)
	mux.HandleFunc("POST /api/scenarios", s.handleCreateScenario)
	mux.HandleFunc("GET /api/scenarios", s.handleListScenarios)
	mux.HandleFunc("GET /api/scenarios/queue", s.handleScenarioQueue)
	mux.HandleFunc("GET /api/scenarios/{id}", s.handleGetScenario)
	assistant := s.assistantProxy()
	mux.Handle("GET /api/assistant/{op}", onlyOps(assistant, "status"))
	mux.Handle("POST /api/assistant/{op}", onlyOps(assistant, "chat"))
	mux.HandleFunc("GET /api/infra", s.handleInfra)
	mux.HandleFunc("GET /api/infra/series", s.handleInfraSeries)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.Handle("GET /metrics", metrics.Handler())
	return s.cors(metrics.Instrument(mux))
}

// onlyOps restricts a {op} route to the listed operations.
func onlyOps(next http.Handler, ops ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, op := range ops {
			if r.PathValue("op") == op {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	if len(s.cfg.AllowedOrigins) == 0 {
		return true
	}
	for _, o := range s.cfg.AllowedOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRaw(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unreachable")
		return
	}
	if !s.ready.isSet() {
		writeError(w, http.StatusServiceUnavailable, "not consuming streams yet")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleCity(w http.ResponseWriter, r *http.Request) {
	city := s.state.City()
	if city == nil {
		writeError(w, http.StatusServiceUnavailable, "no simulation run yet")
		return
	}
	w.Header().Set("ETag", `"`+s.state.RunID()+`"`)
	writeRaw(w, city)
}

func (s *Server) handleGetClock(w http.ResponseWriter, r *http.Request) {
	clk := s.state.Clock()
	if clk == nil {
		writeError(w, http.StatusServiceUnavailable, "no simulation run yet")
		return
	}
	writeRaw(w, clk)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) queueControl(w http.ResponseWriter, r *http.Request, c events.Control) {
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, _ := json.Marshal(c)
	id, err := s.rdb.XAdd(r.Context(), &redis.XAddArgs{
		Stream: events.StreamControl, MaxLen: bus.MaxLen[events.StreamControl], Approx: true,
		Values: map[string]any{"data": data},
	}).Result()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not queue command")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "id": id, "command": c})
}

func (s *Server) handlePostClock(w http.ResponseWriter, r *http.Request) {
	var c events.Control
	if !decodeBody(w, r, &c) {
		return
	}
	if c.Action == "reset" {
		writeError(w, http.StatusBadRequest, "use POST /api/sim/reset to reset")
		return
	}
	s.queueControl(w, r, c)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Seed *int64 `json:"seed"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &body) {
		return
	}
	s.queueControl(w, r, events.Control{Action: "reset", Seed: body.Seed})
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	raw := s.state.LatestTelemetry()
	if raw == nil {
		writeError(w, http.StatusServiceUnavailable, "no telemetry yet")
		return
	}
	writeRaw(w, raw)
}

func (s *Server) handleTelemetryAt(w http.ResponseWriter, r *http.Request) {
	at, err := time.Parse(time.RFC3339, r.URL.Query().Get("at"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "query parameter 'at' must be an RFC 3339 time")
		return
	}
	raw := s.state.TelemetryAt(at)
	if raw == nil {
		writeError(w, http.StatusNotFound, "no telemetry at or before that time in this run")
		return
	}
	writeRaw(w, raw)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if !slices.Contains(HistoryKinds, kind) {
		writeError(w, http.StatusBadRequest, "kind must be one of "+strings.Join(HistoryKinds, ", "))
		return
	}
	minutes := 180
	if q := r.URL.Query().Get("minutes"); q != "" {
		m, err := strconv.Atoi(q)
		if err != nil || m < 1 || m > 1440 {
			writeError(w, http.StatusBadRequest, "minutes must be an integer between 1 and 1440")
			return
		}
		minutes = m
	}
	series, found := s.state.History(kind, id, minutes)
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no %s with id %q", kind, id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "id": id, "minutes": minutes, "points": series})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		l, err := strconv.Atoi(q)
		if err != nil || l < 1 || l > 300 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 300")
			return
		}
		limit = l
	}
	writeJSON(w, http.StatusOK, s.state.Events(limit))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	var hello [][]byte
	info, _ := json.Marshal(map[string]string{"run_id": s.state.RunID(), "version": s.cfg.Version})
	hello = append(hello, wrap("hello", info))
	if c := s.state.Clock(); c != nil {
		hello = append(hello, wrap("clock", c))
	}
	if t := s.state.LatestTelemetry(); t != nil {
		hello = append(hello, wrap("telemetry", t))
	}
	if f := s.state.Frame(); f != nil {
		hello = append(hello, wrap("frame", f))
	}
	for _, raw := range s.ml.latestForecasts() {
		hello = append(hello, wrap("forecast", raw))
	}
	if a := s.ml.latestAnomalies(); a != nil {
		hello = append(hello, wrap("anomaly", a))
	}
	s.hub.Serve(w, r, hello)
}

// ---------------------------------------------------------------- runtime

// RuntimeInfo is the real status of the running services. In Phase 1 it
// covers the gateway process, Redis and the simulation worker heartbeat;
// Kubernetes telemetry is added in Phase 5.
type RuntimeInfo struct {
	Mode      string    `json:"mode"` // local | kubernetes
	CheckedAt time.Time `json:"checked_at"`
	Gateway   struct {
		Version    string  `json:"version"`
		Hostname   string  `json:"hostname"`
		UptimeS    float64 `json:"uptime_s"`
		WSClients  int     `json:"ws_clients"`
		WSSent     int64   `json:"ws_messages_sent"`
		WSDropped  int64   `json:"ws_messages_dropped"`
		Goroutines int     `json:"goroutines"`
		HeapMB     float64 `json:"heap_mb"`
		Snapshots  int     `json:"telemetry_snapshots"`
	} `json:"gateway"`
	Redis struct {
		OK           bool             `json:"ok"`
		LatencyMs    float64          `json:"latency_ms"`
		UsedMemoryMB float64          `json:"used_memory_mb"`
		Streams      map[string]int64 `json:"streams"`
		Error        string           `json:"error,omitempty"`
	} `json:"redis"`
	Simulation struct {
		Alive         bool              `json:"alive"`
		HeartbeatAgeS *float64          `json:"heartbeat_age_s"`
		Heartbeat     map[string]string `json:"heartbeat"`
	} `json:"simulation"`
	Database struct {
		Configured bool    `json:"configured"`
		OK         bool    `json:"ok"`
		LatencyMs  float64 `json:"latency_ms"`
		Error      string  `json:"error,omitempty"`
	} `json:"database"`
	ScenarioQueue *QueueInfo `json:"scenario_queue"`
}

func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var info RuntimeInfo
	info.Mode = "local"
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		info.Mode = "kubernetes"
	}
	info.CheckedAt = time.Now().UTC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	g := &info.Gateway
	g.Version = s.cfg.Version
	g.Hostname, _ = os.Hostname()
	g.UptimeS = time.Since(s.started).Round(time.Second).Seconds()
	g.WSClients = s.hub.Clients()
	g.WSSent = s.hub.sent.Load()
	g.WSDropped = s.hub.dropped.Load()
	g.Goroutines = runtime.NumGoroutine()
	g.HeapMB = float64(ms.HeapAlloc) / (1 << 20)
	g.Snapshots = s.state.Range().Count

	start := time.Now()
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		info.Redis.Error = err.Error()
	} else {
		info.Redis.OK = true
		info.Redis.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		info.Redis.Streams = map[string]int64{}
		for _, st := range []string{events.StreamFrames, events.StreamTelemetry, events.StreamEvents, events.StreamClock, events.StreamControl} {
			n, _ := s.rdb.XLen(ctx, st).Result()
			info.Redis.Streams[st] = n
		}
		if mem, err := s.rdb.Info(ctx, "memory").Result(); err == nil {
			info.Redis.UsedMemoryMB = parseUsedMemory(mem)
		}
		hb, err := s.rdb.HGetAll(ctx, events.KeyHeartbeat).Result()
		if err == nil && len(hb) > 0 {
			info.Simulation.Heartbeat = hb
			if ts, err := time.Parse(time.RFC3339Nano, hb["ts"]); err == nil {
				age := time.Since(ts).Seconds()
				info.Simulation.HeartbeatAgeS = &age
				info.Simulation.Alive = age < 10
			}
		}
	}
	if s.cfg.Store != nil {
		info.Database.Configured = true
		t0 := time.Now()
		if err := s.cfg.Store.Pool.Ping(ctx); err != nil {
			info.Database.Error = err.Error()
		} else {
			info.Database.OK = true
			info.Database.LatencyMs = float64(time.Since(t0).Microseconds()) / 1000
		}
	}
	if q, err := s.queueInfo(ctx); err == nil {
		info.ScenarioQueue = &q
	}
	writeJSON(w, http.StatusOK, info)
}

func parseUsedMemory(info string) float64 {
	sc := bufio.NewScanner(strings.NewReader(info))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "used_memory:"); ok {
			b, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
			return b / (1 << 20)
		}
	}
	return 0
}
