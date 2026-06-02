package control

import (
	"testing"
	"time"

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
