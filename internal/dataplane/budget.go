package dataplane

import (
	"errors"

	"github.com/vanshamara/Augur/internal/core"
)

var ErrOverBudget = errors.New("no backend fits the request cost budget")

// BackendPrice is the per-token price the gateway uses to estimate request cost
// before it calls a backend.
type BackendPrice struct {
	InputPerToken   float64
	OutputPerToken  float64
	MaxOutputTokens int
}

func copyPricing(pricing map[core.BackendID]BackendPrice) map[core.BackendID]BackendPrice {
	if len(pricing) == 0 {
		return nil
	}
	out := make(map[core.BackendID]BackendPrice, len(pricing))
	for id, price := range pricing {
		out[id] = price
	}
	return out
}

// estimateMaxCostUSD returns the largest cost the request could reach on one
// backend. It returns false when the backend has no price.
func (g *Gateway) estimateMaxCostUSD(req core.Request, id core.BackendID) (float64, bool) {
	price, ok := g.pricing[id]
	if !ok {
		return 0, false
	}
	inputCost := float64(req.Features.PromptTokens) * price.InputPerToken
	outputCost := float64(outputTokenBound(req, price)) * price.OutputPerToken
	return inputCost + outputCost, true
}

func outputTokenBound(req core.Request, price BackendPrice) int {
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	if price.MaxOutputTokens > 0 {
		return price.MaxOutputTokens
	}
	return 0
}

// applyBudget drops candidates whose estimated cost is over the request budget.
// When every primary candidate is too expensive it reports a clear over-budget
// error instead of a generic empty candidate set.
func (g *Gateway) applyBudget(req core.Request, candidates candidateSet) candidateSet {
	budget := req.Features.CostBudget
	if budget <= 0 {
		return candidates
	}

	affordable := g.affordableBackends(req, candidates.IDs, budget)
	candidates.Fallbacks = g.affordableBackends(req, candidates.Fallbacks, budget)
	if len(candidates.IDs) > 0 && len(affordable) == 0 && len(candidates.Fallbacks) == 0 {
		candidates.IDs = nil
		candidates.Err = ErrOverBudget
		return candidates
	}
	candidates.IDs = affordable
	return candidates
}

func (g *Gateway) affordableBackends(req core.Request, candidates []core.BackendID, budget float64) []core.BackendID {
	out := make([]core.BackendID, 0, len(candidates))
	for _, id := range candidates {
		cost, ok := g.estimateMaxCostUSD(req, id)
		if !ok {
			if !g.requirePricing {
				out = append(out, id)
			}
			continue
		}
		if cost <= budget {
			out = append(out, id)
		}
	}
	return out
}
