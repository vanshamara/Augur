package control

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/learn"
)

type SharedStore struct {
	mu          sync.Mutex
	available   bool
	configHash  string
	snapshot    LinearSnapshot
	checkpoints map[string]LinearSnapshot
	tau         time.Duration
}

func NewSharedStore(config DistributedConfig) *SharedStore {
	config = normalizeDistributedConfig(config)
	return &SharedStore{
		available:   true,
		configHash:  config.ConfigHash,
		snapshot:    EmptyLinearSnapshot(config.Backends, config.Dimension, config.Start),
		checkpoints: map[string]LinearSnapshot{},
		tau:         config.Tau,
	}
}

func (s *SharedStore) SetAvailable(available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.available = available
}

func (s *SharedStore) MergeDelta(configHash string, delta LinearSnapshot, at time.Time) (LinearSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.available || configHash != s.configHash {
		return LinearSnapshot{}, false
	}
	s.snapshot = s.snapshot.Add(delta, at, s.tau)
	return s.snapshot.Decayed(at, s.tau), true
}

func (s *SharedStore) Snapshot(configHash string, at time.Time) (LinearSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.available || configHash != s.configHash {
		return LinearSnapshot{}, false
	}
	return s.snapshot.Decayed(at, s.tau), true
}

func (s *SharedStore) SaveCheckpoint(configHash string, snapshot LinearSnapshot, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if configHash != s.configHash {
		return false
	}
	s.checkpoints[configHash] = snapshot.Decayed(at, s.tau)
	return true
}

func (s *SharedStore) LoadCheckpoint(configHash string, at time.Time) (LinearSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.checkpoints[configHash]
	if !ok || configHash != s.configHash {
		return LinearSnapshot{}, false
	}
	return snapshot.Decayed(at, s.tau), true
}

type DistributedConfig struct {
	Backends       []core.BackendID
	Dimension      int
	Start          time.Time
	Tau            time.Duration
	PriorPrecision float64
	InitialMean    float64
	ConfigHash     string
	Clock          clock.Clock
}

type DistributedLinearModel struct {
	core           *learn.Core[distributedState, distributedEvent]
	backends       []core.BackendID
	dimension      int
	tau            time.Duration
	priorPrecision float64
	initialMean    float64
	configHash     string
}

type distributedState struct {
	Baseline LinearSnapshot
	Delta    LinearSnapshot
}

type distributedEvent struct {
	observation *LinearObservation
	baseline    *LinearSnapshot
	resetDelta  bool
	at          time.Time
}

func NewDistributedLinearModel(config DistributedConfig, store *SharedStore) *DistributedLinearModel {
	config = normalizeDistributedConfig(config)
	baseline := EmptyLinearSnapshot(config.Backends, config.Dimension, config.Start)
	if store != nil {
		if checkpoint, ok := store.LoadCheckpoint(config.ConfigHash, config.Start); ok {
			baseline = checkpoint
		}
	}

	model := &DistributedLinearModel{
		backends:       append([]core.BackendID(nil), config.Backends...),
		dimension:      config.Dimension,
		tau:            config.Tau,
		priorPrecision: config.PriorPrecision,
		initialMean:    config.InitialMean,
		configHash:     config.ConfigHash,
	}
	initial := distributedState{
		Baseline: baseline,
		Delta:    EmptyLinearSnapshot(config.Backends, config.Dimension, config.Start),
	}
	model.core = learn.NewCore(initial, model.apply)
	return model
}

func (m *DistributedLinearModel) Update(observation LinearObservation) {
	if observation.Weight <= 0 {
		observation.Weight = 1
	}
	m.core.Update(distributedEvent{observation: &observation})
}

func (m *DistributedLinearModel) Snapshot(at time.Time) LinearSnapshot {
	state := m.core.Snapshot()
	return state.Baseline.Add(state.Delta, at, m.tau)
}

func (m *DistributedLinearModel) Delta(at time.Time) LinearSnapshot {
	state := m.core.Snapshot()
	return state.Delta.Decayed(at, m.tau)
}

func (m *DistributedLinearModel) Sync(store *SharedStore, at time.Time) bool {
	if store == nil {
		return false
	}

	synced := false
	m.core.Transform(func(state distributedState) distributedState {
		delta := state.Delta.Decayed(at, m.tau)
		if delta.TotalUpdates(at, m.tau) == 0 {
			if baseline, ok := store.Snapshot(m.configHash, at); ok {
				state.Baseline = baseline
				state.Delta = EmptyLinearSnapshot(m.backends, m.dimension, at)
				synced = true
			}
			return state
		}
		baseline, ok := store.MergeDelta(m.configHash, delta, at)
		if !ok {
			return state
		}
		state.Baseline = baseline
		state.Delta = EmptyLinearSnapshot(m.backends, m.dimension, at)
		synced = true
		return state
	})
	return synced
}

func (m *DistributedLinearModel) StartSyncLoop(ctx context.Context, store *SharedStore, cadence time.Duration, clk clock.Clock) {
	if cadence <= 0 {
		cadence = time.Second
	}
	if clk == nil {
		clk = clock.NewReal()
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-clk.After(cadence):
				m.Sync(store, clk.Now())
			}
		}
	}()
}

func (m *DistributedLinearModel) Flush() {
	m.core.Flush()
}

func (m *DistributedLinearModel) Close() {
	m.core.Close()
}

func (m *DistributedLinearModel) apply(state distributedState, event distributedEvent) distributedState {
	if event.observation != nil {
		state.Delta = applyObservationToSnapshot(state.Delta, *event.observation, m.dimension, m.tau)
		return state
	}
	if event.baseline != nil {
		state.Baseline = *event.baseline
		if event.resetDelta {
			state.Delta = EmptyLinearSnapshot(m.backends, m.dimension, event.at)
		}
	}
	return state
}

func ConfigHash(backends []core.BackendID, policyID string, dimension int, tau time.Duration) string {
	ids := make([]string, len(backends))
	for i, id := range backends {
		ids[i] = string(id)
	}
	sort.Strings(ids)

	hash := fnv.New64a()
	hash.Write([]byte(strings.Join(ids, ",")))
	hash.Write([]byte("|"))
	hash.Write([]byte(policyID))
	hash.Write([]byte("|"))
	hash.Write([]byte(fmt.Sprintf("%d|%d", dimension, tau.Nanoseconds())))
	return fmt.Sprintf("%x", hash.Sum64())
}

func normalizeDistributedConfig(config DistributedConfig) DistributedConfig {
	if config.Dimension <= 0 {
		config.Dimension = FeatureDimension
	}
	if config.Tau <= 0 {
		config.Tau = time.Minute
	}
	if config.PriorPrecision <= 0 {
		config.PriorPrecision = 1
	}
	if config.ConfigHash == "" {
		config.ConfigHash = ConfigHash(config.Backends, "default", config.Dimension, config.Tau)
	}
	return config
}
