package control

import (
	"context"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
)

func TestDistributedSyncPushesDeltaNotTotals(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := distributedTestConfig(start)
	store := NewSharedStore(config)
	replica := NewDistributedLinearModel(config, store)
	defer replica.Close()

	replica.Update(distributedObservation("a", 10, start))
	replica.Flush()
	if !replica.Sync(store, start) {
		t.Fatal("first sync should succeed")
	}
	first := replica.Snapshot(start).TotalUpdates(start, config.Tau)

	if !replica.Sync(store, start) {
		t.Fatal("empty second sync should still adopt the baseline")
	}
	second := replica.Snapshot(start).TotalUpdates(start, config.Tau)
	if first != second {
		t.Fatalf("empty sync should not double count, first=%v second=%v", first, second)
	}
}

func TestDistributedSyncSharesStateAcrossReplicas(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := distributedTestConfig(start)
	store := NewSharedStore(config)
	writer := NewDistributedLinearModel(config, store)
	reader := NewDistributedLinearModel(config, store)
	defer writer.Close()
	defer reader.Close()

	writer.Update(distributedObservation("a", 10, start))
	writer.Flush()
	writer.Sync(store, start)
	reader.Sync(store, start)

	prediction := reader.Snapshot(start).Predict("a", []float64{1}, start, config.Tau, config.PriorPrecision, config.InitialMean, false)
	if prediction.Count == 0 {
		t.Fatal("reader should learn from the shared baseline")
	}
}

func TestDistributedStoreUnavailableKeepsLocalDelta(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := distributedTestConfig(start)
	store := NewSharedStore(config)
	store.SetAvailable(false)
	replica := NewDistributedLinearModel(config, store)
	defer replica.Close()

	replica.Update(distributedObservation("a", 10, start))
	replica.Flush()
	if replica.Sync(store, start) {
		t.Fatal("sync should fail while the store is down")
	}
	if replica.Delta(start).TotalUpdates(start, config.Tau) == 0 {
		t.Fatal("local delta should remain available while the store is down")
	}

	store.SetAvailable(true)
	if !replica.Sync(store, start) {
		t.Fatal("sync should succeed when the store comes back")
	}
	if replica.Delta(start).TotalUpdates(start, config.Tau) != 0 {
		t.Fatal("delta should reset after a successful sync")
	}
}

func TestCheckpointRequiresMatchingConfigHash(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := distributedTestConfig(start)
	store := NewSharedStore(config)
	snapshot := EmptyLinearSnapshot(config.Backends, config.Dimension, start)
	if !store.SaveCheckpoint(config.ConfigHash, snapshot, start) {
		t.Fatal("matching config should save a checkpoint")
	}
	if _, ok := store.LoadCheckpoint("wrong", start); ok {
		t.Fatal("wrong config hash should not load a checkpoint")
	}
	if store.SaveCheckpoint("wrong", snapshot, start) {
		t.Fatal("non matching checkpoint should be rejected")
	}
}

func TestAsyncSyncLoopUsesVirtualClock(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	config := distributedTestConfig(start)
	store := NewSharedStore(config)
	replica := NewDistributedLinearModel(config, store)
	defer replica.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replica.StartSyncLoop(ctx, store, time.Second, clk)

	replica.Update(distributedObservation("a", 10, start))
	replica.Flush()
	time.Sleep(5 * time.Millisecond)
	clk.Advance(time.Second)
	waitForSync(t, func() bool {
		return replica.Delta(clk.Now()).TotalUpdates(clk.Now(), config.Tau) == 0
	})
}

func distributedTestConfig(start time.Time) DistributedConfig {
	ids := []core.BackendID{"a", "b"}
	tau := time.Minute
	return DistributedConfig{
		Backends:       ids,
		Dimension:      1,
		Start:          start,
		Tau:            tau,
		PriorPrecision: 1,
		ConfigHash:     ConfigHash(ids, "test", 1, tau),
	}
}

func distributedObservation(id core.BackendID, value float64, at time.Time) LinearObservation {
	return LinearObservation{
		Backend:      id,
		Features:     []float64{1},
		Value:        value,
		Weight:       1,
		At:           at,
		DecisionTime: at,
	}
}

func waitForSync(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("sync did not finish")
}
