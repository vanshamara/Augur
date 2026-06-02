package control

import (
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

type BackendStats struct {
	P95Ms        float64
	HasP95       bool
	ErrorRate    float64
	HasErrorRate bool
}

type Prediction struct {
	Mean     float64
	Variance float64
	Count    float64
}

type QualityReader interface {
	Predict(id core.BackendID, req core.Request, at time.Time) Prediction
}

type Gate struct {
	policy *Policy
}

type GateDecision struct {
	Candidates []core.BackendID
	Infeasible bool
}

func NewGate(policy *Policy) *Gate {
	return &Gate{policy: policy}
}

// Filter applies policy constraints before the bandit sees candidates.
func (g *Gate) Filter(req core.Request, candidates []core.BackendID, stats map[core.BackendID]BackendStats, quality QualityReader, at time.Time) GateDecision {
	feasible := make([]core.BackendID, 0, len(candidates))
	for _, id := range candidates {
		if g.isFeasible(req, id, stats[id], quality, at) {
			feasible = append(feasible, id)
		}
	}
	if len(feasible) > 0 {
		return GateDecision{Candidates: feasible}
	}
	if g.policy.config.OnInfeasible == InfeasibleFailClosed {
		return GateDecision{Infeasible: true}
	}
	return GateDecision{Candidates: append([]core.BackendID(nil), candidates...), Infeasible: true}
}

func (g *Gate) isFeasible(req core.Request, id core.BackendID, stats BackendStats, quality QualityReader, at time.Time) bool {
	constraints := g.policy.config.Constraints
	if stats.HasP95 && constraints.MaxP95Ms > 0 && stats.P95Ms > constraints.MaxP95Ms {
		return false
	}
	if stats.HasErrorRate && constraints.MaxErrorRate > 0 && stats.ErrorRate > constraints.MaxErrorRate {
		return false
	}
	if constraints.MinQuality <= 0 || quality == nil {
		return true
	}

	prediction := quality.Predict(id, req, at)
	if prediction.Count == 0 && coldStartAllowed(g.policy.config.Exploration.ColdStartBudget, req.ID, id) {
		return true
	}
	return qualityStatistic(prediction, constraints.QualityGate) >= constraints.MinQuality
}

func qualityStatistic(prediction Prediction, gate GateStatistic) float64 {
	mean := clamp01(prediction.Mean)
	width := confidenceWidth(prediction.Variance)
	switch gate {
	case GateOnLCB:
		return clamp01(mean - width)
	case GateOnUCB:
		return clamp01(mean + width)
	default:
		return mean
	}
}

func coldStartAllowed(budget float64, requestID string, backendID core.BackendID) bool {
	if budget <= 0 {
		return false
	}
	if budget >= 1 {
		return true
	}
	value := rng.HashKey(requestID + "/" + string(backendID))
	fraction := float64(value) / float64(^uint64(0))
	return fraction < budget
}
