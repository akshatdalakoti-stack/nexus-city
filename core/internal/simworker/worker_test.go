package simworker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"nexuscity/internal/bus"
	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/sim"
)

func newTestWorker(t *testing.T) (*Worker, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	cfg := DefaultConfig()
	cfg.Speed = 60
	return New(cfg, rdb), rdb
}

// Changing speed and pausing must not change the simulation's outcome:
// the worker at tick N has the same state as a bare engine stepped N times.
func TestSpeedChangesDoNotAffectResults(t *testing.T) {
	ctx := context.Background()
	w, _ := newTestWorker(t)
	if err := w.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	step := func(dt time.Duration) { now = now.Add(dt); w.Advance(ctx, dt, now) }
	for i := 0; i < 20; i++ {
		step(700 * time.Millisecond)
	}
	w.Apply(ctx, events.Control{Action: "speed", Speed: 5})
	for i := 0; i < 13; i++ {
		step(330 * time.Millisecond)
	}
	w.Apply(ctx, events.Control{Action: "pause"})
	step(5 * time.Second) // paused: no progress
	w.Apply(ctx, events.Control{Action: "resume"})
	w.Apply(ctx, events.Control{Action: "speed", Speed: 60})
	for i := 0; i < 9; i++ {
		step(time.Second)
	}

	tick := w.Engine().Tick
	if tick == 0 {
		t.Fatal("worker did not advance")
	}
	ref := sim.New(city.Generate(42))
	ref.StepN(int(tick))
	if ref.StateHash() != w.Engine().StateHash() {
		t.Fatalf("state at tick %d differs from a plain run", tick)
	}
}

func TestPausedWorkerDoesNotStep(t *testing.T) {
	ctx := context.Background()
	w, _ := newTestWorker(t)
	if err := w.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	w.Apply(ctx, events.Control{Action: "pause"})
	w.Advance(ctx, time.Second, time.Now())
	if w.Engine().Tick != 0 {
		t.Fatalf("paused worker advanced to tick %d", w.Engine().Tick)
	}
}

func TestPublishesToStreams(t *testing.T) {
	ctx := context.Background()
	w, rdb := newTestWorker(t)
	if err := w.Reset(ctx, 7); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 10; i++ { // 10 wall seconds at 60× = 10 simulated minutes
		now = now.Add(time.Second)
		w.Advance(ctx, time.Second, now)
	}
	n, _ := rdb.XLen(ctx, events.StreamTelemetry).Result()
	if n != 11 { // tick-0 observation + one per simulated minute
		t.Fatalf("telemetry stream has %d entries, want 11", n)
	}
	msgs, _ := rdb.XRevRangeN(ctx, events.StreamTelemetry, "+", "-", 1).Result()
	data, _ := bus.Data(msgs[0])
	var env events.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Schema != events.SchemaTelemetry || env.RunID != w.RunID() || env.Seed != 7 || env.Tick != 600 {
		t.Fatalf("unexpected envelope %+v", env)
	}
	var tel events.Telemetry
	if err := json.Unmarshal(env.Payload, &tel); err != nil {
		t.Fatalf("payload does not match the telemetry schema: %v", err)
	}
	if len(tel.Roads) != len(w.Engine().City.Roads) {
		t.Fatal("payload roads not aligned with the city")
	}
	if ok, _ := rdb.Exists(ctx, events.KeyCity, events.KeyClock, events.KeyHeartbeat).Result(); ok != 3 {
		t.Fatalf("expected city, clock and heartbeat keys, found %d", ok)
	}
}

// Commands written to the control stream reach the running worker.
func TestControlQueueIsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, rdb := newTestWorker(t)
	w.cfg.TickInterval = 10 * time.Millisecond
	go func() { _ = w.Run(ctx) }()

	waitFor(t, func() bool { return rdb.Exists(ctx, events.KeyClock).Val() == 1 })
	data, _ := json.Marshal(events.Control{Action: "speed", Speed: 2})
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: events.StreamControl, Values: map[string]any{"data": data}})
	clockSpeed := func() float64 {
		raw, err := rdb.Get(ctx, events.KeyClock).Bytes()
		if err != nil {
			return 0
		}
		var env events.Envelope
		var clk events.Clock
		if json.Unmarshal(raw, &env) != nil || json.Unmarshal(env.Payload, &clk) != nil {
			return 0
		}
		return clk.Speed
	}
	waitFor(t, func() bool { return clockSpeed() == 2 })

	// An invalid command is ignored rather than crashing the worker; a valid
	// one after it is still processed.
	bad, _ := json.Marshal(events.Control{Action: "speed", Speed: 3})
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: events.StreamControl, Values: map[string]any{"data": bad}})
	good, _ := json.Marshal(events.Control{Action: "speed", Speed: 5})
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: events.StreamControl, Values: map[string]any{"data": good}})
	waitFor(t, func() bool { return clockSpeed() == 5 })
}

func TestResetStartsNewRun(t *testing.T) {
	ctx := context.Background()
	w, rdb := newTestWorker(t)
	if err := w.Reset(ctx, 1); err != nil {
		t.Fatal(err)
	}
	first := w.RunID()
	w.Advance(ctx, time.Second, time.Now())
	seed := int64(9)
	time.Sleep(2 * time.Millisecond) // run ids carry millisecond timestamps
	w.Apply(ctx, events.Control{Action: "reset", Seed: &seed})
	if w.RunID() == first || w.Engine().Seed != 9 || w.Engine().Tick != 0 {
		t.Fatalf("reset did not start a fresh run: %s seed=%d tick=%d", w.RunID(), w.Engine().Seed, w.Engine().Tick)
	}
	if n := rdb.XLen(ctx, events.StreamTelemetry).Val(); n != 1 {
		t.Fatalf("old telemetry not cleared: %d entries", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
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

// A restarted worker continues the same run, in exactly the same state.
func TestRestartResumesTheRun(t *testing.T) {
	ctx := context.Background()
	a, rdb := newTestWorker(t)
	if err := a.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 20; i++ { // 20 simulated minutes at 60x
		now = now.Add(time.Second)
		a.Advance(ctx, time.Second, now)
	}
	cfg := DefaultConfig()
	b := New(cfg, rdb)
	ok, err := b.resume(ctx)
	if err != nil || !ok {
		t.Fatalf("resume: %v %v", ok, err)
	}
	if b.RunID() != a.RunID() {
		t.Fatalf("resumed run %s, want %s", b.RunID(), a.RunID())
	}
	if b.Engine().Tick < 19*60 {
		t.Fatalf("resumed at tick %d, too far behind", b.Engine().Tick)
	}
	ref := sim.New(city.Generate(42))
	ref.StepN(int(b.Engine().Tick))
	if ref.StateHash() != b.Engine().StateHash() {
		t.Fatal("resumed state differs from a plain run at the same tick")
	}
	if n := rdb.XLen(ctx, events.StreamTelemetry).Val(); n != 21 {
		t.Fatalf("resume must not clear or republish history: %d telemetry entries", n)
	}

	other := DefaultConfig()
	other.Seed = 7
	if ok, _ := New(other, rdb).resume(ctx); ok {
		t.Fatal("resumed a run with a different seed")
	}
}
