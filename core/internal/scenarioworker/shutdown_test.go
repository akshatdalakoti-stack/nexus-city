package scenarioworker

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"nexuscity/internal/events"
)

// An idle worker leaves the consumer group when it stops, so autoscaled
// replicas don't pile up as dead consumers; a busy one keeps its entry so
// its unfinished scenario can be reclaimed.
func TestWorkerLeavesGroupOnShutdownOnlyWhenIdle(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	consumers := func() int {
		cs, err := rdb.XInfoConsumers(ctx, events.StreamScenarios, events.GroupScenarioWorkers).Result()
		if err != nil {
			t.Fatal(err)
		}
		return len(cs)
	}

	idle := New(Config{Consumer: "idle-worker"}, rdb, nil)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = idle.Run(runCtx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cs, _ := rdb.XInfoConsumers(ctx, events.StreamScenarios, events.GroupScenarioWorkers).Result(); len(cs) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if n := consumers(); n != 0 {
		t.Fatalf("idle worker should leave the group, %d consumers remain", n)
	}

	// A worker holding an unacknowledged message stays registered.
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: events.StreamScenarios, Values: map[string]any{"id": "sc-x"}})
	rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: events.GroupScenarioWorkers, Consumer: "busy-worker",
		Streams: []string{events.StreamScenarios, ">"}, Count: 1})
	New(Config{Consumer: "busy-worker"}, rdb, nil).leaveGroup()
	if n := consumers(); n != 1 {
		t.Fatalf("busy worker must keep its pending entry, got %d consumers", n)
	}
}
