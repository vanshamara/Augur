package router

import (
	"context"
	"math"
	"sync/atomic"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

type EnvoyLeastRequest struct {
	inFlight map[core.BackendID]*atomic.Int64
	weights  map[core.BackendID]float64
	bias     float64
	deriver  *rng.Deriver
}

func NewEnvoyLeastRequest(ids []core.BackendID, weights map[core.BackendID]float64, seed uint64) *EnvoyLeastRequest {
	inFlight := make(map[core.BackendID]*atomic.Int64, len(ids))
	for _, id := range ids {
		inFlight[id] = &atomic.Int64{}
	}
	return &EnvoyLeastRequest{
		inFlight: inFlight,
		weights:  copyWeights(weights),
		bias:     1,
		deriver:  rng.NewDeriver(seed),
	}
}

func (e *EnvoyLeastRequest) Name() string {
	return "envoy-least-request"
}

func (e *EnvoyLeastRequest) Pick(ctx context.Context, req core.Request, candidates []core.BackendID) core.BackendID {
	if len(candidates) == 1 {
		e.inFlight[candidates[0]].Add(1)
		return candidates[0]
	}
	if e.sameWeights(candidates) {
		return e.pickLeastOfTwo(req, candidates)
	}
	return e.pickWeighted(candidates)
}

func (e *EnvoyLeastRequest) Observe(ctx context.Context, choice core.BackendID, resp core.Response) {
	counter := e.inFlight[choice]
	if counter != nil {
		counter.Add(-1)
	}
}

func (e *EnvoyLeastRequest) pickLeastOfTwo(req core.Request, candidates []core.BackendID) core.BackendID {
	gen := e.deriver.Rand(rng.HashKey(req.ID), rng.HashKey("envoy-least-request"))
	first := gen.IntN(len(candidates))
	second := gen.IntN(len(candidates) - 1)
	if second >= first {
		second++
	}

	best := candidates[first]
	other := candidates[second]
	if e.load(other) < e.load(best) {
		best = other
	}
	e.inFlight[best].Add(1)
	return best
}

func (e *EnvoyLeastRequest) pickWeighted(candidates []core.BackendID) core.BackendID {
	best := candidates[0]
	bestScore := e.effectiveWeight(best)
	for _, id := range candidates[1:] {
		score := e.effectiveWeight(id)
		if score > bestScore {
			best = id
			bestScore = score
		}
	}
	e.inFlight[best].Add(1)
	return best
}

func (e *EnvoyLeastRequest) sameWeights(candidates []core.BackendID) bool {
	first := e.weightFor(candidates[0])
	for _, id := range candidates[1:] {
		if e.weightFor(id) != first {
			return false
		}
	}
	return true
}

func (e *EnvoyLeastRequest) effectiveWeight(id core.BackendID) float64 {
	return e.weightFor(id) / math.Pow(float64(e.load(id)+1), e.bias)
}

func (e *EnvoyLeastRequest) load(id core.BackendID) int64 {
	counter := e.inFlight[id]
	if counter == nil {
		return 0
	}
	return counter.Load()
}

func (e *EnvoyLeastRequest) weightFor(id core.BackendID) float64 {
	if e.weights == nil {
		return 1
	}
	weight, ok := e.weights[id]
	if !ok {
		return 1
	}
	if weight <= 0 {
		return 1
	}
	return weight
}

func copyWeights(values map[core.BackendID]float64) map[core.BackendID]float64 {
	if values == nil {
		return nil
	}
	out := make(map[core.BackendID]float64, len(values))
	for id, value := range values {
		out[id] = value
	}
	return out
}
