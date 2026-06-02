package live

import (
	"context"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/quality"
)

type fakeBackend struct {
	id core.BackendID
}

func (b fakeBackend) ID() core.BackendID {
	return b.id
}

func (b fakeBackend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	return core.Response{
		RequestID:  req.ID,
		Backend:    b.id,
		OutputText: "answer",
		Outcome: core.Outcome{
			LatencyMs:    100,
			CostUSD:      0.01,
			OutputTokens: 8,
		},
	}, nil
}

type fakeScorer struct {
	score float64
	calls int
}

func (s *fakeScorer) Score(ctx context.Context, req core.Request, resp core.Response) (quality.Result, error) {
	s.calls++
	return quality.Result{Score: s.score, Reason: "test"}, nil
}

func TestLearnerUpdatesRewardAndQualityFromLiveResponse(t *testing.T) {
	clk := clock.NewVirtual(time.Unix(0, 0))
	bandit := control.NewBanditRouter(control.BanditConfig{
		Policy: control.NewPolicy(control.PolicyConfig{
			Exploration: control.ExplorationConfig{
				JudgeSampleRate: 1,
			},
		}),
		Backends: []core.BackendID{"a"},
		Clock:    clk,
	})
	gateway, err := dataplane.New(dataplane.Config{
		Router:   bandit,
		Backends: []backend.Backend{fakeBackend{id: "a"}},
		Clock:    clk,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	scorer := &fakeScorer{score: 0.42}
	learner, err := New(Config{Gateway: gateway, Bandit: bandit, Scorer: scorer, Seed: 7})
	if err != nil {
		t.Fatalf("new learner: %v", err)
	}
	defer learner.Close()

	req := core.Request{
		ID:     "req-1",
		Prompt: "hello",
		Features: core.Features{
			Type:         core.Chat,
			PromptTokens: 10,
		},
	}
	resp, err := learner.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("call learner: %v", err)
	}
	learner.Flush()

	if resp.Backend != "a" {
		t.Fatalf("backend got %s", resp.Backend)
	}
	if _, ok := bandit.Attribution().Decision("req-1"); !ok {
		t.Fatal("decision should be recorded")
	}
	if _, ok := bandit.Attribution().Response("req-1"); !ok {
		t.Fatal("response should be recorded")
	}
	if scorer.calls != 1 {
		t.Fatalf("scorer calls got %d", scorer.calls)
	}

	reward := bandit.RewardModel().Snapshot().Arms["a"]
	if reward.Updates <= 0 {
		t.Fatalf("reward updates got %v", reward.Updates)
	}
	qualityPrediction := bandit.QualityModel().Predict("a", req, clk.Now())
	if qualityPrediction.Count <= 0 {
		t.Fatalf("quality count got %v", qualityPrediction.Count)
	}
}

func TestLearnerSkipsQualityWhenPropensityIsZero(t *testing.T) {
	clk := clock.NewVirtual(time.Unix(0, 0))
	bandit := control.NewBanditRouter(control.BanditConfig{
		Policy:   control.NewPolicy(control.PolicyConfig{}),
		Backends: []core.BackendID{"a"},
		Clock:    clk,
	})
	gateway, err := dataplane.New(dataplane.Config{
		Router:   bandit,
		Backends: []backend.Backend{fakeBackend{id: "a"}},
		Clock:    clk,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	scorer := &fakeScorer{score: 0.42}
	learner, err := New(Config{Gateway: gateway, Bandit: bandit, Scorer: scorer, Seed: 7})
	if err != nil {
		t.Fatalf("new learner: %v", err)
	}
	defer learner.Close()

	_, err = learner.Call(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("call learner: %v", err)
	}
	learner.Flush()

	if scorer.calls != 0 {
		t.Fatalf("scorer calls got %d", scorer.calls)
	}
}
