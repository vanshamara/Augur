package router

import (
	"context"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

type LiteLLMShuffle struct {
	weights map[core.BackendID]float64
	deriver *rng.Deriver
}

func NewLiteLLMShuffle(weights map[core.BackendID]float64, seed uint64) *LiteLLMShuffle {
	return &LiteLLMShuffle{
		weights: copyWeights(weights),
		deriver: rng.NewDeriver(seed),
	}
}

func (l *LiteLLMShuffle) Name() string {
	return "litellm-shuffle"
}

func (l *LiteLLMShuffle) Pick(ctx context.Context, req core.Request, candidates []core.BackendID) core.BackendID {
	total := 0.0
	for _, id := range candidates {
		total += l.weightFor(id)
	}
	if total <= 0 {
		return candidates[0]
	}

	gen := l.deriver.Rand(rng.HashKey(req.ID), rng.HashKey("litellm-shuffle"))
	choice := gen.Float64() * total
	seen := 0.0
	for _, id := range candidates {
		seen += l.weightFor(id)
		if choice < seen {
			return id
		}
	}
	return candidates[len(candidates)-1]
}

func (l *LiteLLMShuffle) Observe(ctx context.Context, choice core.BackendID, resp core.Response) {
}

func (l *LiteLLMShuffle) weightFor(id core.BackendID) float64 {
	if l.weights == nil {
		return 1
	}
	weight, ok := l.weights[id]
	if !ok {
		return 1
	}
	if weight < 0 {
		return 0
	}
	return weight
}
