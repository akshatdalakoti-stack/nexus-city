package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/bus"
	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/scenario"
	"nexuscity/internal/store"
)

var cityCache sync.Map // seed -> *city.City

func cachedCity(seed int64) *city.City {
	if c, ok := cityCache.Load(seed); ok {
		return c.(*city.City)
	}
	c := city.Generate(seed)
	cityCache.Store(seed, c)
	return c
}

// CreateScenarioRequest is the body of POST /api/scenarios.
type CreateScenarioRequest struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Params      scenario.Params `json:"params"`
	DurationMin int             `json:"duration_min"`
	// BaseTick defaults to the live run's current time (rounded down to 5
	// minutes). The live seed is always used.
	BaseTick *int64 `json:"base_tick,omitempty"`
}

func (s *Server) needDB(w http.ResponseWriter) bool {
	if s.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "scenarios need PostgreSQL: set DATABASE_URL for the gateway")
		return false
	}
	return true
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "sc-" + hex.EncodeToString(b)
}

func defaultName(req CreateScenarioRequest) string {
	p := req.Params
	switch req.Kind {
	case scenario.RoadClosure:
		return "Close " + strings.Join(p.RoadIDs, ", ")
	case scenario.RushHour:
		return fmt.Sprintf("Rush hour +%.0f%%", p.DemandPct)
	case scenario.SignalTiming:
		return "Adaptive signal timing"
	case scenario.IndustrialSpike:
		return fmt.Sprintf("Industrial emissions ×%.1f in %s", p.EmissionFactor, p.DistrictID)
	case scenario.Heatwave:
		return fmt.Sprintf("Heatwave +%.0f °C", p.TempDeltaC)
	}
	return req.Kind
}

func (s *Server) handleCreateScenario(w http.ResponseWriter, r *http.Request) {
	if !s.needDB(w) {
		return
	}
	var req CreateScenarioRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	clockRaw := s.state.Clock()
	if clockRaw == nil {
		writeError(w, http.StatusServiceUnavailable, "no live simulation run to branch from")
		return
	}
	var env events.Envelope
	var clk events.Clock
	if json.Unmarshal(clockRaw, &env) != nil || json.Unmarshal(env.Payload, &clk) != nil {
		writeError(w, http.StatusInternalServerError, "unreadable clock")
		return
	}
	base := clk.Tick - clk.Tick%scenario.SampleEvery
	if req.BaseTick != nil {
		base = *req.BaseTick
	}
	if req.DurationMin == 0 {
		req.DurationMin = 120
	}
	spec := scenario.Spec{Kind: req.Kind, Seed: clk.Seed, BaseTick: base, DurationMin: req.DurationMin, Params: req.Params}
	if err := spec.Validate(cachedCity(clk.Seed)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultName(req)
	}
	if len(name) > 120 {
		name = name[:120]
	}
	params, _ := json.Marshal(req.Params)
	runID := clk.RunID
	sc := &store.Scenario{ID: newID(), Name: name, Kind: req.Kind, Params: params, Seed: clk.Seed, BaseRunID: &runID,
		BaseTick: base, DurationMin: req.DurationMin, SpecHash: spec.Hash(), Status: "queued", CreatedAt: time.Now().UTC()}
	if err := s.cfg.Store.CreateScenario(r.Context(), sc); err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not store the scenario")
		return
	}
	if err := s.rdb.XAdd(r.Context(), &redis.XAddArgs{Stream: events.StreamScenarios, MaxLen: 5000, Approx: true,
		Values: map[string]any{"id": sc.ID}}).Err(); err != nil {
		_ = s.cfg.Store.Fail(r.Context(), sc.ID, "could not queue: "+err.Error())
		writeError(w, http.StatusServiceUnavailable, "could not queue the scenario")
		return
	}
	payload, _ := json.Marshal(events.ScenarioUpdate{ID: sc.ID, Status: "queued"})
	_, _ = bus.Publish(r.Context(), s.rdb, events.StreamScenarioUpdates, events.Envelope{Schema: events.SchemaScenario,
		RunID: runID, Seed: clk.Seed, Tick: base, EmittedAt: time.Now().UTC(), Payload: payload})
	writeJSON(w, http.StatusAccepted, sc)
}

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	if !s.needDB(w) {
		return
	}
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		l, err := strconv.Atoi(q)
		if err != nil || l < 1 || l > 200 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 200")
			return
		}
		limit = l
	}
	list, err := s.cfg.Store.ListScenarios(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	if !s.needDB(w) {
		return
	}
	sc, err := s.cfg.Store.GetScenario(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such scenario")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

// QueueInfo describes the scenario queue (the KEDA scaling signal in Phase 5).
type QueueInfo struct {
	Waiting   int64 `json:"waiting"`   // not yet picked up by any worker
	Running   int64 `json:"running"`   // delivered to a worker, not yet acknowledged
	Consumers int64 `json:"consumers"` // workers that have joined the group
}

func (s *Server) queueInfo(ctx context.Context) (QueueInfo, error) {
	groups, err := s.rdb.XInfoGroups(ctx, events.StreamScenarios).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return QueueInfo{}, nil
		}
		return QueueInfo{}, err
	}
	for _, g := range groups {
		if g.Name == events.GroupScenarioWorkers {
			return QueueInfo{Waiting: g.Lag, Running: g.Pending, Consumers: g.Consumers}, nil
		}
	}
	n, _ := s.rdb.XLen(ctx, events.StreamScenarios).Result()
	return QueueInfo{Waiting: n}, nil
}

func (s *Server) handleScenarioQueue(w http.ResponseWriter, r *http.Request) {
	q, err := s.queueInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unavailable")
		return
	}
	writeJSON(w, http.StatusOK, q)
}
