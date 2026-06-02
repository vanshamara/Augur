package quality

import (
	"context"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

type MockConfig struct {
	Seed           uint64
	BackendQuality map[core.BackendID]float64
	Noise          float64
}

type MockScorer struct {
	seed           uint64
	backendQuality map[core.BackendID]float64
	noise          float64
}

func NewMockScorer(config MockConfig) *MockScorer {
	return &MockScorer{
		seed:           config.Seed,
		backendQuality: copyQuality(config.BackendQuality),
		noise:          config.Noise,
	}
}

func (m *MockScorer) Score(ctx context.Context, req core.Request, resp core.Response) (Result, error) {
	score := 0.85
	if configured, ok := m.backendQuality[resp.Backend]; ok {
		score = configured
	}
	if resp.Errored {
		score = 0
	}
	if m.noise > 0 {
		gen := rng.NewDeriver(m.seed).Rand(rng.HashKey(req.ID), rng.HashKey(string(resp.Backend)), rng.HashKey("quality"))
		score += m.noise * (2*gen.Float64() - 1)
	}
	return Result{Score: clamp01(score), Reason: "mock"}, nil
}

func copyQuality(values map[core.BackendID]float64) map[core.BackendID]float64 {
	out := make(map[core.BackendID]float64, len(values))
	for id, value := range values {
		out[id] = value
	}
	return out
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
