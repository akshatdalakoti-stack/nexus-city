package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/events"
)

var forecastModels = []string{"traffic", "energy", "pm25"}

// ModelStatus is what the gateway reports for one model: the serving
// process's own report (from Redis) plus live accuracy measured here.
type ModelStatus struct {
	Kind         string            `json:"kind"`
	Service      json.RawMessage   `json:"service"`
	Alive        bool              `json:"alive"`
	LiveAccuracy []AccuracySummary `json:"live_accuracy"`
}

const liveAccuracyWindowS = 6 * 3600 // six simulated hours

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	hash, err := s.rdb.HGetAll(ctx, events.KeyModels).Result()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unavailable")
		return
	}
	out := []ModelStatus{}
	for _, kind := range []string{"traffic", "energy", "pm25", "anomaly"} {
		raw, ok := hash[kind]
		st := ModelStatus{Kind: kind, Service: json.RawMessage("null"), LiveAccuracy: []AccuracySummary{}}
		if ok {
			st.Service = json.RawMessage(raw)
			var info struct {
				UpdatedAt time.Time `json:"updated_at"`
				Version   *string   `json:"version"`
			}
			if json.Unmarshal([]byte(raw), &info) == nil {
				st.Alive = info.Version != nil && time.Since(info.UpdatedAt) < 30*time.Second
			}
		}
		if kind != "anomaly" {
			st.LiveAccuracy = s.ml.accuracySummary(kind, liveAccuracyWindowS)
		}
		out = append(out, st)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleModelMeta serves the training metadata (metrics, features, data)
// the model service published for the version it is running.
func (s *Server) handleModelMeta(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !slices.Contains([]string{"traffic", "energy", "pm25", "anomaly"}, kind) {
		writeError(w, http.StatusBadRequest, "kind must be traffic, energy, pm25 or anomaly")
		return
	}
	raw, err := s.rdb.Get(r.Context(), events.KeyModelMeta+kind).Bytes()
	if errors.Is(err, redis.Nil) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no %s model is being served", kind))
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unavailable")
		return
	}
	writeRaw(w, raw)
}

func (s *Server) handleForecastsLatest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ml.latestForecasts())
}

func (s *Server) handleEntityForecasts(w http.ResponseWriter, r *http.Request) {
	model, id := r.PathValue("model"), r.PathValue("id")
	if !slices.Contains(forecastModels, model) {
		writeError(w, http.StatusBadRequest, "model must be traffic, energy or pm25")
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
	var since int64
	if latest := s.state.LatestTelemetry(); latest != nil {
		var env events.Envelope
		if json.Unmarshal(latest, &env) == nil {
			since = env.Tick - int64(minutes)*60
		}
	}
	points, found := s.ml.entityForecasts(model, id, since)
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no %s forecasts for %q", model, id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": model, "id": id, "minutes": minutes, "points": points})
}

func (s *Server) handleAnomaliesLatest(w http.ResponseWriter, r *http.Request) {
	raw := s.ml.latestAnomalies()
	if raw == nil {
		writeError(w, http.StatusServiceUnavailable, "no anomaly scores yet (is the anomaly service running?)")
		return
	}
	writeRaw(w, raw)
}
