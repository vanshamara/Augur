package control

import (
	"context"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
)

type fakeQuality struct {
	values map[core.BackendID]Prediction
}

func (f fakeQuality) Predict(id core.BackendID, req core.Request, at time.Time) Prediction {
	return f.values[id]
}

func TestPolicyRewardKeepsConstraintsOutOfReward(t *testing.T) {
	policy := NewPolicy(PolicyConfig{
		Objective: ObjectiveConfig{Type: MinimizeLatency},
		Constraints: ConstraintConfig{
			MinQuality: 0.99,
		},
	})

	resp := core.Response{Backend: "a", Outcome: core.Outcome{LatencyMs: 250, CostUSD: 10}}
	if got := policy.Reward(resp); got != -250 {
		t.Fatalf("reward should only use latency objective, got %v", got)
	}

	resp.Errored = true
	if got := policy.Reward(resp); got != BadOutcomeReward {
		t.Fatalf("bad outcomes should hit the hard floor, got %v", got)
	}
}

func TestBlendObjectiveBalancesLatencyAndCostInMillionths(t *testing.T) {
	policy := NewPolicy(PolicyConfig{
		Objective: ObjectiveConfig{
			Type:          BlendObjective,
			LatencyWeight: 0.1,
			CostWeight:    1,
		},
	})

	resp := core.Response{Outcome: core.Outcome{LatencyMs: 1000, CostUSD: 0.0001}}
	if got := policy.ObjectiveCost(resp); got != 200 {
		t.Fatalf("objective cost got %v", got)
	}
}

func TestQualityGateCanPreferHigherQualityOverLowerCost(t *testing.T) {
	policy := NewPolicy(PolicyConfig{
		Constraints: ConstraintConfig{
			MinQuality: 0.85,
		},
		Objective: ObjectiveConfig{
			Type:          BlendObjective,
			LatencyWeight: 0.1,
			CostWeight:    1,
		},
		Exploration: ExplorationConfig{
			ColdStartBudget: 0,
		},
	})
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	bandit := NewBanditRouter(BanditConfig{
		Policy:   policy,
		Backends: []core.BackendID{"cheap", "strong"},
		Clock:    clk,
	})
	defer bandit.Close()

	req := core.Request{
		ID: "req-1",
		Features: core.Features{
			PromptTokens:    1200,
			Type:            core.Reasoning,
			LatencyBudgetMs: 3000,
			CostBudget:      0.08,
			UserTier:        "premium",
		},
	}
	features := EncodeFeatures(req)
	for i := 0; i < 40; i++ {
		bandit.QualityModel().Update(LinearObservation{
			Backend:  "cheap",
			Features: features,
			Value:    0.20,
			Weight:   1,
			At:       start,
		})
		bandit.QualityModel().Update(LinearObservation{
			Backend:  "strong",
			Features: features,
			Value:    0.95,
			Weight:   1,
			At:       start,
		})
	}
	bandit.QualityModel().Flush()

	got := bandit.Pick(context.Background(), req, []core.BackendID{"cheap", "strong"})
	if got != "strong" {
		t.Fatalf("quality floor should keep cheap out, got %s", got)
	}
}

func TestGateFiltersByConstraints(t *testing.T) {
	policy := NewPolicy(PolicyConfig{
		Constraints: ConstraintConfig{
			MaxP95Ms:     1000,
			MinQuality:   0.85,
			MaxErrorRate: 0.05,
		},
	})
	gate := NewGate(policy)
	req := request("req-1")

	decision := gate.Filter(
		req,
		[]core.BackendID{"fast", "slow", "bad"},
		map[core.BackendID]BackendStats{
			"fast": {P95Ms: 400, HasP95: true, ErrorRate: 0.01, HasErrorRate: true},
			"slow": {P95Ms: 1400, HasP95: true, ErrorRate: 0.01, HasErrorRate: true},
			"bad":  {P95Ms: 400, HasP95: true, ErrorRate: 0.10, HasErrorRate: true},
		},
		fakeQuality{values: map[core.BackendID]Prediction{
			"fast": {Mean: 0.90, Count: 10},
			"slow": {Mean: 0.90, Count: 10},
			"bad":  {Mean: 0.90, Count: 10},
		}},
		time.Now(),
	)

	if len(decision.Candidates) != 1 || decision.Candidates[0] != "fast" {
		t.Fatalf("expected only fast to pass, got %v", decision.Candidates)
	}
}

func TestGateDefinesEmptySetBehavior(t *testing.T) {
	req := request("req-1")
	candidates := []core.BackendID{"a"}
	stats := map[core.BackendID]BackendStats{
		"a": {P95Ms: 2000, HasP95: true},
	}

	bestEffort := NewGate(NewPolicy(PolicyConfig{
		Constraints: ConstraintConfig{MaxP95Ms: 1000},
	}))
	decision := bestEffort.Filter(req, candidates, stats, nil, time.Now())
	if !decision.Infeasible || len(decision.Candidates) != 1 {
		t.Fatalf("best effort should return original candidates, got %+v", decision)
	}

	failClosed := NewGate(NewPolicy(PolicyConfig{
		Constraints:  ConstraintConfig{MaxP95Ms: 1000},
		OnInfeasible: InfeasibleFailClosed,
	}))
	decision = failClosed.Filter(req, candidates, stats, nil, time.Now())
	if !decision.Infeasible || len(decision.Candidates) != 0 {
		t.Fatalf("fail closed should return no candidates, got %+v", decision)
	}
}

func TestColdStartProbationCanAdmitUnknownQuality(t *testing.T) {
	policy := NewPolicy(PolicyConfig{
		Constraints: ConstraintConfig{MinQuality: 0.99},
		Exploration: ExplorationConfig{
			ColdStartBudget: 1,
		},
	})
	gate := NewGate(policy)
	decision := gate.Filter(
		request("req-1"),
		[]core.BackendID{"new"},
		nil,
		fakeQuality{values: map[core.BackendID]Prediction{
			"new": {Mean: 0.10, Count: 0},
		}},
		time.Now(),
	)

	if len(decision.Candidates) != 1 || decision.Candidates[0] != "new" {
		t.Fatalf("cold-start probation should admit the new backend, got %v", decision.Candidates)
	}
}

func request(id string) core.Request {
	return core.Request{
		ID: id,
		Features: core.Features{
			PromptTokens:    100,
			Type:            core.Chat,
			LatencyBudgetMs: 1000,
			CostBudget:      0.05,
		},
	}
}
