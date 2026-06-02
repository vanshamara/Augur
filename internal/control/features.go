package control

import (
	"math"

	"github.com/vanshamara/Augur/internal/core"
)

const FeatureDimension = 7

func EncodeFeatures(req core.Request) []float64 {
	features := make([]float64, FeatureDimension)
	features[0] = 1
	features[1] = math.Log1p(float64(req.Features.PromptTokens)) / 10
	features[2] = normalizedBudget(req.Features.LatencyBudgetMs, 1000)
	features[3] = req.Features.CostBudget

	switch req.Features.Type {
	case core.Chat:
		features[4] = 1
	case core.Reasoning:
		features[5] = 1
	case core.Embedding:
		features[6] = 1
	}

	return features
}

func normalizedBudget(value int, divisor float64) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / divisor
}
