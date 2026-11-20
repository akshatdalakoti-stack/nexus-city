package gateway

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"nexuscity/internal/events"
)

// snapshot is one telemetry message. Only the parsed form is kept (a raw
// copy would double memory); it is re-encoded when served. env.Payload is nil.
type snapshot struct {
	env events.Envelope
	tel *events.Telemetry
}

// encode rebuilds the envelope JSON for a stored snapshot.
func (sn snapshot) encode() []byte {
	env := sn.env
	env.Payload, _ = json.Marshal(sn.tel)
	raw, _ := json.Marshal(env)
	return raw
}

// State is the gateway's in-memory view of the current run.
type State struct {
	mu        sync.RWMutex
	runID     string
	runSince  time.Time // EmittedAt of the first message of this run
	city      []byte
	clock     []byte // latest clock envelope
	telemetry []snapshot
	latestRaw []byte   // newest telemetry envelope as received
	events    [][]byte // newest last
	frame     []byte
	maxHist   int
	maxEvents int
}

// NewState creates an empty state holding up to maxHist telemetry snapshots.
func NewState(maxHist int) *State {
	return &State{maxHist: maxHist, maxEvents: 300}
}

// RunID returns the current run id.
func (s *State) RunID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runID
}

// beginRun clears everything belonging to the previous run.
func (s *State) beginRun(runID string, since time.Time, city []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runID, s.runSince, s.city = runID, since, city
	s.telemetry, s.latestRaw, s.events, s.frame, s.clock = nil, nil, nil, nil, nil
}

// stale reports whether a message belongs to an older run than the current one.
func (s *State) stale(env events.Envelope) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return env.RunID != s.runID && env.EmittedAt.Before(s.runSince)
}

func (s *State) isCurrent(runID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return runID == s.runID
}

func (s *State) setClock(raw []byte) {
	s.mu.Lock()
	s.clock = raw
	s.mu.Unlock()
}

func (s *State) setFrame(raw []byte) {
	s.mu.Lock()
	s.frame = raw
	s.mu.Unlock()
}

func (s *State) addEvent(raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, raw)
	if over := len(s.events) - s.maxEvents; over > 0 {
		s.events = append([][]byte(nil), s.events[over:]...)
	}
}

func (s *State) addTelemetry(env events.Envelope, raw []byte) (*events.Telemetry, error) {
	var tel events.Telemetry
	if err := json.Unmarshal(env.Payload, &tel); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.telemetry); n > 0 && s.telemetry[n-1].tel.Tick >= tel.Tick {
		return nil, nil // duplicate or out of order
	}
	env.Payload = nil
	s.telemetry = append(s.telemetry, snapshot{env: env, tel: &tel})
	s.latestRaw = raw
	if over := len(s.telemetry) - s.maxHist; over > 0 {
		s.telemetry = append([]snapshot(nil), s.telemetry[over:]...)
	}
	return &tel, nil
}

// TelemetryByTick returns the parsed snapshot at exactly this tick, or nil.
// Callers must not modify it.
func (s *State) TelemetryByTick(tick int64) *events.Telemetry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := sort.Search(len(s.telemetry), func(i int) bool { return s.telemetry[i].tel.Tick >= tick })
	if i < len(s.telemetry) && s.telemetry[i].tel.Tick == tick {
		return s.telemetry[i].tel
	}
	return nil
}

// Snapshot accessors.

func (s *State) City() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.city
}

func (s *State) Clock() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clock
}

func (s *State) Frame() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.frame
}

func (s *State) LatestTelemetry() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestRaw
}

// TelemetryAt returns the latest snapshot at or before t.
func (s *State) TelemetryAt(t time.Time) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := sort.Search(len(s.telemetry), func(i int) bool { return s.telemetry[i].tel.SimTime.After(t) })
	if i == 0 {
		return nil
	}
	return s.telemetry[i-1].encode()
}

// Range describes the replayable window.
type Range struct {
	RunID string     `json:"run_id"`
	From  *time.Time `json:"from"`
	To    *time.Time `json:"to"`
	Count int        `json:"count"`
}

func (s *State) Range() Range {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := Range{RunID: s.runID, Count: len(s.telemetry)}
	if n := len(s.telemetry); n > 0 {
		from, to := s.telemetry[0].tel.SimTime, s.telemetry[n-1].tel.SimTime
		r.From, r.To = &from, &to
	}
	return r
}

// Events returns up to limit recent events, newest first.
func (s *State) Events(limit int) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []json.RawMessage{}
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.events[i])
	}
	return out
}

// HistoryKinds lists the entity kinds History accepts.
var HistoryKinds = []string{"city", "road", "node", "district", "station", "hospital"}

type point[T any] struct {
	T time.Time `json:"t"`
	V T         `json:"v"`
}

// History returns the time series of one entity over the last `minutes`
// simulated minutes. found is false if the entity does not exist.
func (s *State) History(kind, id string, minutes int) (series any, found bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.telemetry)
	if n == 0 {
		return []any{}, kind == "city"
	}
	cutoff := s.telemetry[n-1].tel.SimTime.Add(-time.Duration(minutes) * time.Minute)
	start := sort.Search(n, func(i int) bool { return s.telemetry[i].tel.SimTime.After(cutoff) })
	snaps := s.telemetry[start:]
	latest := s.telemetry[n-1].tel

	switch kind {
	case "city":
		return collect(snaps, -1, func(t *events.Telemetry, _ int) (events.Summary, bool) { return t.Summary, true }), true
	case "road":
		return seriesByID(snaps, latest.Roads, id, func(t *events.Telemetry) []events.RoadObs { return t.Roads },
			func(o events.RoadObs) string { return o.ID })
	case "node":
		return seriesByID(snaps, latest.Nodes, id, func(t *events.Telemetry) []events.NodeObs { return t.Nodes },
			func(o events.NodeObs) string { return o.ID })
	case "district":
		return seriesByID(snaps, latest.Districts, id, func(t *events.Telemetry) []events.DistrictObs { return t.Districts },
			func(o events.DistrictObs) string { return o.ID })
	case "station":
		return seriesByID(snaps, latest.Stations, id, func(t *events.Telemetry) []events.StationObs { return t.Stations },
			func(o events.StationObs) string { return o.ID })
	case "hospital":
		return seriesByID(snaps, latest.Hospitals, id, func(t *events.Telemetry) []events.HospitalObs { return t.Hospitals },
			func(o events.HospitalObs) string { return o.ID })
	}
	return nil, false
}

func collect[T any](snaps []snapshot, idx int, get func(*events.Telemetry, int) (T, bool)) []point[T] {
	out := make([]point[T], 0, len(snaps))
	for _, sn := range snaps {
		if v, ok := get(sn.tel, idx); ok {
			out = append(out, point[T]{T: sn.tel.SimTime, V: v})
		}
	}
	return out
}

// seriesByID finds the entity's position in the latest snapshot (arrays are
// ordered the same way throughout a run) and reads that position from each
// snapshot, checking the id still matches.
func seriesByID[T any](snaps []snapshot, latest []T, id string, list func(*events.Telemetry) []T, idOf func(T) string) (any, bool) {
	idx := -1
	for i, o := range latest {
		if idOf(o) == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, false
	}
	return collect(snaps, idx, func(t *events.Telemetry, i int) (T, bool) {
		items := list(t)
		if i < len(items) && idOf(items[i]) == id {
			return items[i], true
		}
		var zero T
		return zero, false
	}), true
}
