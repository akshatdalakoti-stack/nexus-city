package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"

	"nexuscity/internal/events"
	"nexuscity/internal/simworker"
)

type harness struct {
	ctx    context.Context
	rdb    *redis.Client
	worker *simworker.Worker
	srv    *Server
	http   *httptest.Server
	now    time.Time
}

// newHarness wires a real worker and gateway to an in-memory Redis.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	cfg := simworker.DefaultConfig()
	cfg.Speed = 60
	w := simworker.New(cfg, rdb)
	if err := w.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Version: "test"}, rdb)
	go srv.Consume(ctx)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	h := &harness{ctx: ctx, rdb: rdb, worker: w, srv: srv, http: ts, now: time.Now()}
	h.waitFor(t, func() bool { return srv.ready.isSet() && srv.State().RunID() == w.RunID() })
	return h
}

// advance runs the worker for wall seconds at its configured speed.
func (h *harness) advance(seconds int) {
	for i := 0; i < seconds; i++ {
		h.now = h.now.Add(time.Second)
		h.worker.Advance(h.ctx, time.Second, h.now)
	}
}

func (h *harness) waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within 5s")
}

func (h *harness) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, h.http.URL+path, strings.NewReader(body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestControlValidation(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		body string
		want int
	}{
		{`{"action":"speed","speed":10}`, http.StatusAccepted},
		{`{"action":"pause"}`, http.StatusAccepted},
		{`{"action":"speed","speed":7}`, http.StatusBadRequest},
		{`{"action":"teleport"}`, http.StatusBadRequest},
		{`{"action":"reset"}`, http.StatusBadRequest}, // resets go through /api/sim/reset
		{`{"action":"pause","extra":1}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got, body := h.do(t, "POST", "/api/clock", c.body); got != c.want {
			t.Errorf("POST /api/clock %s → %d (%s), want %d", c.body, got, body, c.want)
		}
	}
	if n := h.rdb.XLen(h.ctx, events.StreamControl).Val(); n != 2 {
		t.Fatalf("control stream has %d commands, want 2 (only valid ones queued)", n)
	}
	if got, _ := h.do(t, "POST", "/api/sim/reset", `{"seed":-4}`); got != http.StatusBadRequest {
		t.Fatalf("negative seed accepted: %d", got)
	}
	if got, _ := h.do(t, "POST", "/api/sim/reset", `{"seed":5}`); got != http.StatusAccepted {
		t.Fatalf("valid reset rejected: %d", got)
	}
}

func TestQueryValidation(t *testing.T) {
	h := newHarness(t)
	for path, want := range map[string]int{
		"/api/history/planet/X":                  http.StatusBadRequest,
		"/api/history/road/R1?minutes=0":         http.StatusBadRequest,
		"/api/history/road/R1?minutes=x":         http.StatusBadRequest,
		"/api/history/road/NOPE":                 http.StatusNotFound,
		"/api/history/road/R1":                   http.StatusOK,
		"/api/telemetry?at=yesterday":            http.StatusBadRequest,
		"/api/telemetry?at=2000-01-01T00:00:00Z": http.StatusNotFound,
		"/api/events?limit=1000":                 http.StatusBadRequest,
		"/api/health":                            http.StatusOK,
		"/api/ready":                             http.StatusOK,
	} {
		if got, body := h.do(t, "GET", path, ""); got != want {
			t.Errorf("GET %s → %d (%s), want %d", path, got, body, want)
		}
	}
}

func TestTelemetryFlowsThroughGateway(t *testing.T) {
	h := newHarness(t)
	h.advance(30) // 30 simulated minutes
	h.waitFor(t, func() bool { return h.srv.State().Range().Count == 31 })

	code, body := h.do(t, "GET", "/api/telemetry/latest", "")
	if code != http.StatusOK {
		t.Fatalf("latest: %d %s", code, body)
	}
	var env events.Envelope
	var tel events.Telemetry
	if err := json.Unmarshal([]byte(body), &env); err != nil || json.Unmarshal(env.Payload, &tel) != nil {
		t.Fatal("latest telemetry does not match the schema")
	}
	if tel.Tick != h.worker.Engine().Latest().Tick {
		t.Fatalf("gateway latest tick %d, worker %d", tel.Tick, h.worker.Engine().Latest().Tick)
	}

	// Replay: a snapshot from the middle of the window.
	at := tel.SimTime.Add(-10 * time.Minute).Format(time.RFC3339)
	code, body = h.do(t, "GET", "/api/telemetry?at="+at, "")
	if code != http.StatusOK || json.Unmarshal([]byte(body), &env) != nil || json.Unmarshal(env.Payload, &tel) != nil {
		t.Fatalf("replay lookup failed: %d", code)
	}
	if tel.Tick != 1200 {
		t.Fatalf("replay returned tick %d, want 1200", tel.Tick)
	}

	// History of one road over the last 15 minutes.
	code, body = h.do(t, "GET", "/api/history/road/R5?minutes=15", "")
	var hist struct {
		Points []struct {
			T time.Time      `json:"t"`
			V events.RoadObs `json:"v"`
		} `json:"points"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &hist) != nil {
		t.Fatalf("history: %d %s", code, body)
	}
	if len(hist.Points) != 15 || hist.Points[0].V.ID != "R5" {
		t.Fatalf("history has %d points (first id %q), want 15 for R5", len(hist.Points), hist.Points[0].V.ID)
	}
}

func TestWebSocketDelivery(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.http.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	read := func() (string, json.RawMessage) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m.Type, m.Data
	}
	if typ, _ := read(); typ != "hello" {
		t.Fatalf("first message %q, want hello", typ)
	}
	h.waitFor(t, func() bool { return h.srv.Hub().Clients() == 1 })

	h.advance(2) // two simulated minutes
	seen := map[string]bool{}
	for !(seen["telemetry"] && seen["frame"]) {
		typ, data := read()
		seen[typ] = true
		if typ == "telemetry" {
			var env events.Envelope
			if err := json.Unmarshal(data, &env); err != nil || env.Schema != events.SchemaTelemetry {
				t.Fatalf("bad telemetry message: %s", data)
			}
		}
	}
}

// The in-memory history must stay capped so the gateway's memory is bounded.
func TestHistoryIsCapped(t *testing.T) {
	h := newHarness(t)
	h.srv.state.maxHist = 20
	h.advance(45) // 45 simulated minutes
	last := h.worker.Engine().Latest().SimTime
	h.waitFor(t, func() bool { // wait until the consumer has caught up
		r := h.srv.State().Range()
		return r.To != nil && r.To.Equal(last)
	})
	if n := h.srv.State().Range().Count; n != 20 {
		t.Fatalf("history holds %d snapshots, cap is 20", n)
	}
	// Re-encoded snapshots must still be valid envelopes.
	r := h.srv.State().Range()
	code, body := h.do(t, "GET", "/api/telemetry?at="+r.From.Format(time.RFC3339), "")
	var env events.Envelope
	var tel events.Telemetry
	if code != http.StatusOK || json.Unmarshal([]byte(body), &env) != nil || json.Unmarshal(env.Payload, &tel) != nil || !tel.SimTime.Equal(*r.From) {
		t.Fatalf("re-encoded snapshot invalid: %d", code)
	}
}

func TestNewRunResetsGatewayState(t *testing.T) {
	h := newHarness(t)
	h.advance(5)
	h.waitFor(t, func() bool { return h.srv.State().Range().Count == 6 })
	old := h.worker.RunID()
	time.Sleep(2 * time.Millisecond)
	seed := int64(3)
	h.worker.Apply(h.ctx, events.Control{Action: "reset", Seed: &seed})
	h.waitFor(t, func() bool { return h.srv.State().RunID() == h.worker.RunID() && h.worker.RunID() != old })
	h.waitFor(t, func() bool { return h.srv.State().Range().Count == 1 })
	var c struct {
		Seed int64 `json:"seed"`
	}
	if err := json.Unmarshal(h.srv.State().City(), &c); err != nil || c.Seed != 3 {
		t.Fatalf("gateway serves the old city after reset (seed %d)", c.Seed)
	}
}
