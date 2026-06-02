package harness

import (
	"math"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

// Oracle answers what the best choice would have been. The expectation oracle uses
// each backend's true mean, which is what a perfectly informed router could match,
// so it measures learning. The realization oracle uses the exact draw for a request
// and is only used to show the band of luck that no router can beat.
type Oracle struct {
	backends []*mock.Backend
	byID     map[core.BackendID]*mock.Backend
}

func NewOracle(backends []*mock.Backend) *Oracle {
	byID := make(map[core.BackendID]*mock.Backend, len(backends))
	for _, backend := range backends {
		byID[backend.ID()] = backend
	}
	return &Oracle{backends: backends, byID: byID}
}

// ExpectedBestLatency returns the lowest true mean latency across backends at the
// given time.
func (o *Oracle) ExpectedBestLatency(at time.Time) float64 {
	best := math.Inf(1)
	for _, b := range o.backends {
		mean := b.TrueParams(at).MeanLatencyMs
		if mean < best {
			best = mean
		}
	}
	return best
}

// RealizedBestLatency returns the lowest actual latency any backend would have
// produced for this exact request at this time.
func (o *Oracle) RealizedBestLatency(req core.Request, at time.Time) float64 {
	best := math.Inf(1)
	for _, b := range o.backends {
		latency := b.Outcome(req, at).LatencyMs
		if latency < best {
			best = latency
		}
	}
	return best
}

type PolicyRegret struct {
	ObjectiveRegret    float64
	LearningCost       float64
	ChosenFeasible     bool
	ViolatedConstraint bool
	Comparable         bool
}

func (o *Oracle) PolicyRegret(req core.Request, choice core.BackendID, at time.Time, policy *control.Policy) PolicyRegret {
	if policy == nil {
		policy = DefaultComparisonPolicy()
	}

	chosen := o.byID[choice]
	if chosen == nil {
		return PolicyRegret{ViolatedConstraint: true}
	}

	chosenParams := chosen.TrueParams(at)
	chosenFeasible := trueFeasible(chosenParams, policy.Config().Constraints)
	oracleObjective, comparable := o.bestExpectedObjective(req, at, policy)
	if !comparable {
		return PolicyRegret{
			ChosenFeasible:     chosenFeasible,
			ViolatedConstraint: !chosenFeasible,
		}
	}

	chosenObjective := expectedObjective(req, chosenParams, policy)
	regret := chosenObjective - oracleObjective
	if regret < 0 && regret > -1e-9 {
		regret = 0
	}

	learningCost := regret
	if learningCost < 0 {
		learningCost = 0
	}

	return PolicyRegret{
		ObjectiveRegret:    regret,
		LearningCost:       learningCost,
		ChosenFeasible:     chosenFeasible,
		ViolatedConstraint: !chosenFeasible,
		Comparable:         true,
	}
}

func (o *Oracle) bestExpectedObjective(req core.Request, at time.Time, policy *control.Policy) (float64, bool) {
	constraints := policy.Config().Constraints
	best := math.Inf(1)
	found := false

	for _, backend := range o.backends {
		params := backend.TrueParams(at)
		if !trueFeasible(params, constraints) {
			continue
		}
		objective := expectedObjective(req, params, policy)
		if objective < best {
			best = objective
			found = true
		}
	}

	if found {
		return best, true
	}
	if policy.Config().OnInfeasible == control.InfeasibleFailClosed {
		return 0, false
	}

	for _, backend := range o.backends {
		objective := expectedObjective(req, backend.TrueParams(at), policy)
		if objective < best {
			best = objective
			found = true
		}
	}
	return best, found
}

func trueFeasible(params mock.Params, constraints control.ConstraintConfig) bool {
	if constraints.MaxP95Ms > 0 && trueP95(params) > constraints.MaxP95Ms {
		return false
	}
	if constraints.MaxErrorRate > 0 && params.ErrorRate > constraints.MaxErrorRate {
		return false
	}
	if constraints.MinQuality > 0 && params.Quality < constraints.MinQuality {
		return false
	}
	return true
}

func expectedObjective(req core.Request, params mock.Params, policy *control.Policy) float64 {
	switch policy.Config().Objective.Type {
	case control.MinimizeCost:
		return expectedCost(req, params)
	case control.BlendObjective:
		config := policy.Config().Objective
		return config.LatencyWeight*params.MeanLatencyMs + config.CostWeight*expectedCost(req, params)*1_000_000
	default:
		return params.MeanLatencyMs
	}
}

func expectedCost(req core.Request, params mock.Params) float64 {
	const expectedOutputTokens = 274.5
	tokens := float64(req.Features.PromptTokens) + expectedOutputTokens
	return tokens * params.CostPerToken
}

func trueP95(params mock.Params) float64 {
	return params.MeanLatencyMs * (1 + 0.9*params.LatencySpread)
}
