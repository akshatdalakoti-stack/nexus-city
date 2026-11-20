// Package simworker runs the simulation engine against the wall clock and
// publishes its output to Redis Streams. It owns the shared simulation
// clock: speed changes and pauses only change how fast steps are taken,
// never what a step does, so results stay reproducible.
package simworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"nexuscity/internal/bus"
	"nexuscity/internal/city"
	"nexuscity/internal/events"
	"nexuscity/internal/metrics"
	"nexuscity/internal/sim"
)

// Config controls the worker.
type Config struct {
	Seed          int64
	Speed         float64
	Paused        bool
	TickInterval  time.Duration // how often the loop wakes up
	FrameInterval time.Duration // minimum gap between vehicle frames
	ClockInterval time.Duration // periodic clock beat
	Hostname      string
	Version       string
	// Resume continues the previous run found in Redis (same seed) instead
	// of starting over. The simulation is deterministic, so fast-forwarding
	// to the last published tick recreates its exact state.
	Resume bool
}

// DefaultConfig returns sensible local-development settings.
func DefaultConfig() Config {
	return Config{Seed: 42, Speed: 10, TickInterval: 100 * time.Millisecond,
		FrameInterval: 200 * time.Millisecond, ClockInterval: 2 * time.Second, Version: "dev", Resume: true}
}

const (
	maxStepsPerTick = 600       // caps catch-up work after a stall
	maxResumeTick   = 7 * 86400 // older runs start over rather than replaying more than a week
)

// Worker drives one simulation run at a time.
type Worker struct {
	cfg     Config
	rdb     *redis.Client
	eng     *sim.Engine
	runID   string
	speed   float64
	paused  bool
	acc     float64
	control chan events.Control

	lastFrameTick           int64
	lastFrame, lastClock    time.Time
	lastBeat                time.Time
	winSteps                int
	winStepTime, winElapsed time.Duration
	stepsPerSec, stepMs     float64
	lastLoop                atomic.Int64 // unix nanos of the last loop iteration
}

// Healthy reports whether the main loop ran within the last few seconds.
// It is safe to call from another goroutine (the health endpoint).
func (w *Worker) Healthy() bool {
	return time.Since(time.Unix(0, w.lastLoop.Load())) < 5*time.Second
}

// New creates a worker. Call Run to start it.
func New(cfg Config, rdb *redis.Client) *Worker {
	return &Worker{cfg: cfg, rdb: rdb, speed: cfg.Speed, paused: cfg.Paused, control: make(chan events.Control, 16)}
}

// RunID identifies the current run.
func (w *Worker) RunID() string { return w.runID }

// Engine exposes the engine for tests.
func (w *Worker) Engine() *sim.Engine { return w.eng }

// Run blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	// Note where the control stream ends *before* announcing the run, so a
	// command sent as soon as the clock appears is not mistaken for an old one.
	start := bus.LastID(ctx, w.rdb, events.StreamControl)
	resumed := false
	if w.cfg.Resume {
		var err error
		if resumed, err = w.resume(ctx); err != nil {
			slog.Warn("could not resume the previous run; starting a new one", "err", err)
		}
	}
	if !resumed {
		if err := w.Reset(ctx, w.cfg.Seed); err != nil {
			return err
		}
	}
	go w.readControl(ctx, start)
	ticker := time.NewTicker(w.cfg.TickInterval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case c := <-w.control:
			w.Apply(ctx, c)
		case now := <-ticker.C:
			dt := now.Sub(last)
			last = now
			w.Advance(ctx, min(dt, time.Second), now)
		}
	}
}

// Reset starts a fresh run with the given seed: it regenerates the city,
// clears the previous run's streams and announces the new run.
func (w *Worker) Reset(ctx context.Context, seed int64) error {
	c := city.Generate(seed)
	w.eng = sim.New(c)
	w.runID = fmt.Sprintf("%d-%s", seed, time.Now().UTC().Format("20060102T150405.000"))
	w.acc = 0
	w.lastFrameTick = -1
	cityJSON, err := json.Marshal(c)
	if err != nil {
		return err
	}
	pipe := w.rdb.TxPipeline()
	pipe.Del(ctx, events.StreamFrames, events.StreamTelemetry, events.StreamEvents)
	pipe.Set(ctx, events.KeyCity, cityJSON, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	slog.Info("simulation run started", "run_id", w.runID, "seed", seed,
		"roads", len(c.Roads), "nodes", len(c.Nodes), "districts", len(c.Districts))
	w.publishClock(ctx, time.Now())
	w.flush(ctx)
	w.publishFrame(ctx, time.Now())
	return nil
}

// Apply executes a control command.
func (w *Worker) Apply(ctx context.Context, c events.Control) {
	if err := c.Validate(); err != nil {
		slog.Warn("ignoring invalid control", "err", err)
		return
	}
	switch c.Action {
	case "pause":
		w.paused = true
	case "resume":
		w.paused = false
	case "speed":
		w.speed = c.Speed
	case "reset":
		seed := w.eng.Seed
		if c.Seed != nil {
			seed = *c.Seed
		}
		if err := w.Reset(ctx, seed); err != nil {
			slog.Error("reset failed", "err", err)
		}
		return
	}
	slog.Info("clock changed", "action", c.Action, "speed", w.speed, "paused", w.paused)
	w.publishClock(ctx, time.Now())
}

// Advance converts elapsed wall time into whole simulation steps.
func (w *Worker) Advance(ctx context.Context, dt time.Duration, now time.Time) {
	w.lastLoop.Store(now.UnixNano())
	if !w.paused {
		w.acc += w.speed * dt.Seconds()
	}
	n := int(w.acc)
	if n > maxStepsPerTick {
		n, w.acc = maxStepsPerTick, 0
	} else {
		w.acc -= float64(n)
	}
	start := time.Now()
	w.eng.StepN(n)
	w.winSteps += n
	stepTime := time.Since(start)
	w.winStepTime += stepTime
	metrics.SimSteps.Add(float64(n))
	metrics.SimStepSeconds.Add(stepTime.Seconds())
	w.winElapsed += dt

	w.flush(ctx)
	if now.Sub(w.lastFrame) >= w.cfg.FrameInterval && w.eng.Tick != w.lastFrameTick {
		w.publishFrame(ctx, now)
	}
	if now.Sub(w.lastClock) >= w.cfg.ClockInterval {
		w.publishClock(ctx, now)
	}
	if now.Sub(w.lastBeat) >= 2*time.Second {
		w.heartbeat(ctx, now)
	}
}

func (w *Worker) envelope(schema string, tick int64, simTime time.Time, payload any) (events.Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return events.Envelope{}, err
	}
	return events.Envelope{Schema: schema, RunID: w.runID, Seed: w.eng.Seed, Tick: tick, SimTime: simTime,
		EmittedAt: time.Now().UTC(), Payload: raw}, nil
}

func (w *Worker) publish(ctx context.Context, stream, schema string, tick int64, simTime time.Time, payload any) {
	env, err := w.envelope(schema, tick, simTime, payload)
	if err == nil {
		_, err = bus.Publish(ctx, w.rdb, stream, env)
	}
	if err != nil && ctx.Err() == nil {
		slog.Error("publish failed", "stream", stream, "err", err)
	}
}

// flush publishes telemetry and events the engine produced since last time.
func (w *Worker) flush(ctx context.Context) {
	for _, t := range w.eng.DrainTelemetry() {
		w.publish(ctx, events.StreamTelemetry, events.SchemaTelemetry, t.Tick, t.SimTime, t)
	}
	for _, ev := range w.eng.DrainEvents() {
		tick := int64(ev.SimTime.Sub(sim.Epoch).Seconds())
		w.publish(ctx, events.StreamEvents, events.SchemaEvent, tick, ev.SimTime, ev)
	}
}

func (w *Worker) publishFrame(ctx context.Context, now time.Time) {
	f := w.eng.Frame()
	w.publish(ctx, events.StreamFrames, events.SchemaFrame, f.Tick, f.SimTime, f)
	w.lastFrame, w.lastFrameTick = now, f.Tick
}

// Clock returns the current clock state.
func (w *Worker) Clock() events.Clock {
	return events.Clock{RunID: w.runID, Seed: w.eng.Seed, Epoch: sim.Epoch, SimTime: w.eng.SimTime(),
		Tick: w.eng.Tick, Speed: w.speed, Paused: w.paused}
}

func (w *Worker) publishClock(ctx context.Context, now time.Time) {
	clk := w.Clock()
	env, err := w.envelope(events.SchemaClock, clk.Tick, clk.SimTime, clk)
	if err != nil {
		return
	}
	raw, _ := json.Marshal(env)
	if err := w.rdb.Set(ctx, events.KeyClock, raw, 0).Err(); err != nil && ctx.Err() == nil {
		slog.Error("set clock failed", "err", err)
	}
	if _, err := bus.Publish(ctx, w.rdb, events.StreamClock, env); err != nil && ctx.Err() == nil {
		slog.Error("publish clock failed", "err", err)
	}
	w.lastClock = now
}

// heartbeat records liveness and throughput so the gateway can report the
// real runtime status. The key expires if the worker stops.
func (w *Worker) heartbeat(ctx context.Context, now time.Time) {
	if w.winElapsed > 0 {
		w.stepsPerSec = float64(w.winSteps) / w.winElapsed.Seconds()
	}
	if w.winSteps > 0 {
		w.stepMs = float64(w.winStepTime.Microseconds()) / 1000 / float64(w.winSteps)
	}
	w.winSteps, w.winStepTime, w.winElapsed = 0, 0, 0
	metrics.SimTick.Set(float64(w.eng.Tick))
	metrics.SimSpeed.Set(w.speed)
	metrics.SimAgents.Set(float64(w.eng.AgentCount()))
	if w.paused {
		metrics.SimPaused.Set(1)
	} else {
		metrics.SimPaused.Set(0)
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	pipe := w.rdb.Pipeline()
	pipe.HSet(ctx, events.KeyHeartbeat, map[string]any{
		"ts":            now.UTC().Format(time.RFC3339Nano),
		"run_id":        w.runID,
		"seed":          w.eng.Seed,
		"tick":          w.eng.Tick,
		"speed":         f(w.speed),
		"paused":        strconv.FormatBool(w.paused),
		"agents":        w.eng.AgentCount(),
		"steps_per_sec": f(w.stepsPerSec),
		"step_ms":       f(w.stepMs),
		"hostname":      w.cfg.Hostname,
		"version":       w.cfg.Version,
	})
	pipe.Expire(ctx, events.KeyHeartbeat, 10*time.Second)
	if _, err := pipe.Exec(ctx); err != nil && ctx.Err() == nil {
		slog.Error("heartbeat failed", "err", err)
	}
	w.lastBeat = now
}

// readControl forwards commands that arrive after id `last` to the loop.
// Commands queued before the worker started are ignored.
func (w *Worker) readControl(ctx context.Context, last string) {
	for ctx.Err() == nil {
		res, err := w.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{events.StreamControl, last}, Block: 2 * time.Second, Count: 10,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("control read failed", "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		for _, s := range res {
			for _, m := range s.Messages {
				last = m.ID
				data, ok := bus.Data(m)
				if !ok {
					continue
				}
				var c events.Control
				if err := json.Unmarshal(data, &c); err != nil {
					slog.Warn("bad control message", "err", err)
					continue
				}
				select {
				case w.control <- c:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// resume continues the run recorded in Redis if it used the same seed. It
// returns false (and changes nothing) when there is nothing to resume.
func (w *Worker) resume(ctx context.Context) (bool, error) {
	raw, err := w.rdb.Get(ctx, events.KeyClock).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var env events.Envelope
	var clk events.Clock
	if err := json.Unmarshal(raw, &env); err != nil {
		return false, err
	}
	if err := json.Unmarshal(env.Payload, &clk); err != nil {
		return false, err
	}
	if clk.Seed != w.cfg.Seed || clk.RunID == "" {
		return false, nil
	}
	tick := clk.Tick
	if msgs, err := w.rdb.XRevRangeN(ctx, events.StreamTelemetry, "+", "-", 1).Result(); err == nil && len(msgs) == 1 {
		if data, ok := bus.Data(msgs[0]); ok {
			var t events.Envelope
			if json.Unmarshal(data, &t) == nil && t.RunID == clk.RunID && t.Tick > tick {
				tick = t.Tick
			}
		}
	}
	if tick <= 0 || tick > maxResumeTick {
		return false, nil
	}
	t0 := time.Now()
	c := city.Generate(clk.Seed)
	cityJSON, err := json.Marshal(c)
	if err != nil {
		return false, err
	}
	e := sim.New(c)
	for e.Tick < tick {
		e.StepN(int(min(tick-e.Tick, 3600)))
		e.DrainTelemetry() // already published before the restart
		e.DrainEvents()
	}
	w.eng, w.runID, w.speed, w.paused = e, clk.RunID, clk.Speed, clk.Paused
	w.acc, w.lastFrameTick = 0, -1
	if err := w.rdb.Set(ctx, events.KeyCity, cityJSON, 0).Err(); err != nil {
		return false, err
	}
	slog.Info("resumed simulation run", "run_id", w.runID, "tick", tick, "sim_time", e.SimTime(),
		"replay_s", time.Since(t0).Round(time.Millisecond).Seconds())
	w.publishClock(ctx, time.Now())
	return true, nil
}
