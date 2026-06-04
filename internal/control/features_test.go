package control

import (
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

func TestEncodeFeaturesIncludesRequestTypeAndTier(t *testing.T) {
	req := core.Request{
		ID: "req-1",
		Features: core.Features{
			PromptTokens:    1000,
			Type:            core.Reasoning,
			LatencyBudgetMs: 2500,
			CostBudget:      0.05,
			UserTier:        "premium",
		},
	}

	features := EncodeFeatures(req)

	if len(features) != FeatureDimension {
		t.Fatalf("feature dimension got %d want %d", len(features), FeatureDimension)
	}
	if features[0] != 1 {
		t.Fatalf("intercept got %v", features[0])
	}
	if features[2] != 2.5 {
		t.Fatalf("latency budget feature got %v", features[2])
	}
	if features[3] != 5 {
		t.Fatalf("cost budget feature got %v", features[3])
	}
	if features[5] != 1 {
		t.Fatalf("reasoning feature got %v", features[5])
	}
	if features[10] != 1 {
		t.Fatalf("premium feature got %v", features[10])
	}
}
