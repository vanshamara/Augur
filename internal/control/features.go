package control

import (
	"math"
	"strings"

	"github.com/vanshamara/Augur/internal/core"
)

const FeatureDimension = 14

func EncodeFeatures(req core.Request) []float64 {
	features := make([]float64, FeatureDimension)
	features[0] = 1
	features[1] = math.Log1p(float64(req.Features.PromptTokens)) / 10
	features[2] = normalizedBudget(req.Features.LatencyBudgetMs, 1000)
	features[3] = normalizedCostBudget(req.Features.CostBudget)

	switch req.Features.Type {
	case core.Chat:
		features[4] = 1
	case core.Reasoning:
		features[5] = 1
	case core.Coding:
		features[6] = 1
	case core.Embedding:
		features[7] = 1
	}

	switch strings.ToLower(strings.TrimSpace(req.Features.UserTier)) {
	case "free":
		features[8] = 1
	case "standard":
		features[9] = 1
	case "premium":
		features[10] = 1
	case "enterprise":
		features[11] = 1
	}
	if latencyCritical(req.Features.LatencyBudgetMs) {
		features[12] = 1
	}
	if costSensitive(req.Features.CostBudget) {
		features[13] = 1
	}

	return features
}

func normalizedBudget(value int, divisor float64) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / divisor
}

func normalizedCostBudget(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return value * 100
}

func latencyCritical(value int) bool {
	return value > 0 && value <= 500
}

func costSensitive(value float64) bool {
	return value > 0 && value <= 0.005
}
