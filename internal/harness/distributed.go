package harness

import (
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

type DistributedLearningConfig struct {
	ReplicaCount int
	Sharing      bool
	StoreDown    bool
	SyncCadence  time.Duration
	Tau          time.Duration
}

type DistributedLearningReport struct {
	Regime                 string
	ReplicaCount           int
	Sharing                bool
	StoreDown              bool
	Requests               int
	DisagreementFraction   float64
	MeanPosteriorKL        float64
	MeanObjectiveRegret    float64
	CumulativeLearningCost float64
}

func RunDistributedLearning(regime Regime, seed uint64, requests int, start time.Time, config DistributedLearningConfig) DistributedLearningReport {
	config = normalizeDistributedLearningConfig(config)
	clk := clock.NewVirtual(start)
	backends := regime.Build(seed, clk, start)
	ids := idsOf(backends)
	byID := byID(backends)
	policy := DefaultComparisonPolicy()
	trace := GenerateTrace(seed, requests, start)
	oracle := NewOracle(backends)

	storeConfig := distributedModelConfig(ids, policy.ID(), start, config.Tau)
	store := control.NewSharedStore(storeConfig)
	store.SetAvailable(!config.StoreDown)
	replicas := make([]*control.DistributedLinearModel, config.ReplicaCount)
	for i := range replicas {
		replicas[i] = control.NewDistributedLinearModel(storeConfig, store)
		defer replicas[i].Close()
	}

	nextSync := start.Add(config.SyncCadence)
	disagreements := 0
	var objectiveRegret float64
	var learningCost float64

	for _, event := range trace.Events {
		advanceTo(clk, event.Arrival)
		if config.Sharing {
			for !nextSync.After(event.Arrival) {
				syncReplicas(replicas, store, nextSync)
				nextSync = nextSync.Add(config.SyncCadence)
			}
		}

		if replicasDisagree(replicas, ids, event.Request, event.Arrival, config.Tau) {
			disagreements++
		}

		replica := replicas[event.Sequence%len(replicas)]
		choice := chooseReplica(replica, ids, event.Request, event.Arrival, config.Tau)
		backend := byID[choice]
		outcome := backend.Outcome(event.Request, event.Arrival)
		resp := core.Response{RequestID: event.Request.ID, Backend: choice, Outcome: outcome}
		regret := oracle.PolicyRegret(event.Request, choice, event.Arrival, policy)
		objectiveRegret += regret.ObjectiveRegret
		learningCost += regret.LearningCost

		replica.Update(control.LinearObservation{
			Backend:      choice,
			Features:     control.EncodeFeatures(event.Request),
			Value:        policy.Reward(resp),
			Weight:       1,
			At:           event.Arrival,
			DecisionTime: event.Arrival,
		})
		replica.Flush()
	}

	if config.Sharing {
		syncReplicas(replicas, store, trace.Events[len(trace.Events)-1].Arrival)
	}

	return DistributedLearningReport{
		Regime:                 regime.Name,
		ReplicaCount:           config.ReplicaCount,
		Sharing:                config.Sharing,
		StoreDown:              config.StoreDown,
		Requests:               requests,
		DisagreementFraction:   float64(disagreements) / float64(requests),
		MeanPosteriorKL:        meanReplicaKL(replicas, trace.Events[len(trace.Events)-1].Arrival, config.Tau),
		MeanObjectiveRegret:    objectiveRegret / float64(requests),
		CumulativeLearningCost: learningCost,
	}
}

func DistributedAxes(regime Regime, seed uint64, requests int, start time.Time) []DistributedLearningReport {
	configs := []DistributedLearningConfig{
		{ReplicaCount: 1, Sharing: false},
		{ReplicaCount: 4, Sharing: false},
		{ReplicaCount: 4, Sharing: true},
	}
	reports := make([]DistributedLearningReport, len(configs))
	for i, config := range configs {
		reports[i] = RunDistributedLearning(regime, seed, requests, start, config)
	}
	return reports
}

func normalizeDistributedLearningConfig(config DistributedLearningConfig) DistributedLearningConfig {
	if config.ReplicaCount <= 0 {
		config.ReplicaCount = 1
	}
	if config.SyncCadence <= 0 {
		config.SyncCadence = time.Second
	}
	if config.Tau <= 0 {
		config.Tau = time.Minute
	}
	return config
}

func distributedModelConfig(ids []core.BackendID, policyID string, start time.Time, tau time.Duration) control.DistributedConfig {
	return control.DistributedConfig{
		Backends:       ids,
		Dimension:      control.FeatureDimension,
		Start:          start,
		Tau:            tau,
		PriorPrecision: 1,
		ConfigHash:     control.ConfigHash(ids, policyID, control.FeatureDimension, tau),
	}
}

func byID(backends []*mock.Backend) map[core.BackendID]*mock.Backend {
	byID := make(map[core.BackendID]*mock.Backend, len(backends))
	for _, backend := range backends {
		byID[backend.ID()] = backend
	}
	return byID
}

func syncReplicas(replicas []*control.DistributedLinearModel, store *control.SharedStore, at time.Time) {
	for _, replica := range replicas {
		replica.Sync(store, at)
	}
}

func replicasDisagree(replicas []*control.DistributedLinearModel, ids []core.BackendID, req core.Request, at time.Time, tau time.Duration) bool {
	if len(replicas) < 2 {
		return false
	}
	first := chooseReplica(replicas[0], ids, req, at, tau)
	for _, replica := range replicas[1:] {
		if chooseReplica(replica, ids, req, at, tau) != first {
			return true
		}
	}
	return false
}

func chooseReplica(replica *control.DistributedLinearModel, ids []core.BackendID, req core.Request, at time.Time, tau time.Duration) core.BackendID {
	snapshot := replica.Snapshot(at)
	return snapshot.BestArm(ids, control.EncodeFeatures(req), at, tau, 1, 0)
}

func meanReplicaKL(replicas []*control.DistributedLinearModel, at time.Time, tau time.Duration) float64 {
	if len(replicas) < 2 {
		return 0
	}
	total := 0.0
	pairs := 0
	for i := 0; i < len(replicas); i++ {
		left := replicas[i].Snapshot(at)
		for j := i + 1; j < len(replicas); j++ {
			right := replicas[j].Snapshot(at)
			total += control.SymmetricKL(left, right, at, tau, 1)
			pairs++
		}
	}
	return total / float64(pairs)
}
