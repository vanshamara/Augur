package live

import (
	"context"
	"io"
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

type fakeStore struct {
	saves []control.LearnedState
}

func (s *fakeStore) Save(state control.LearnedState) error {
	s.saves = append(s.saves, state)
	return nil
}

type fakeStreamGateway struct {
	streamCalls int
	stream      core.Stream
}

func (g *fakeStreamGateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	return core.Response{RequestID: req.ID, Backend: "a"}, nil
}

func (g *fakeStreamGateway) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	g.streamCalls++
	return g.stream, nil
}

type fakeLiveStream struct {
	chunks []core.StreamChunk
	index  int
}

func (s *fakeLiveStream) Recv() (core.StreamChunk, error) {
	if s.index >= len(s.chunks) {
		return core.StreamChunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *fakeLiveStream) Close() error {
	return nil
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

func TestLearnerPersistsLearnedState(t *testing.T) {
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
	store := &fakeStore{}
	learner, err := New(Config{Gateway: gateway, Bandit: bandit, Store: store, SaveEvery: 1})
	if err != nil {
		t.Fatalf("new learner: %v", err)
	}
	defer learner.Close()

	_, err = learner.Call(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("call learner: %v", err)
	}
	learner.Flush()

	if len(store.saves) == 0 {
		t.Fatal("learned state should be saved")
	}
	last := store.saves[len(store.saves)-1]
	if last.Reward.Arms["a"].Updates <= 0 {
		t.Fatalf("saved reward updates got %v", last.Reward.Arms["a"].Updates)
	}
}

func TestLearnerPersistsQualityStateAfterJudge(t *testing.T) {
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
	store := &fakeStore{}
	scorer := &fakeScorer{score: 0.92}
	learner, err := New(Config{Gateway: gateway, Bandit: bandit, Scorer: scorer, Store: store, Seed: 7, SaveEvery: 1})
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
	_, err = learner.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("call learner: %v", err)
	}
	learner.Flush()

	if len(store.saves) == 0 {
		t.Fatal("learned state should be saved")
	}
	last := store.saves[len(store.saves)-1]
	if last.Quality.Arms["a"].Updates <= 0 {
		t.Fatalf("saved quality updates got %v", last.Quality.Arms["a"].Updates)
	}
}

func TestLearnerStreamsThroughGatewayAndPersistsState(t *testing.T) {
	bandit := control.NewBanditRouter(control.BanditConfig{
		Policy:   control.NewPolicy(control.PolicyConfig{}),
		Backends: []core.BackendID{"a"},
	})
	gateway := &fakeStreamGateway{
		stream: &fakeLiveStream{
			chunks: []core.StreamChunk{
				{Delta: "hello"},
				{Done: true, Outcome: core.Outcome{LatencyMs: 10, OutputTokens: 1}},
			},
		},
	}
	store := &fakeStore{}
	learner, err := New(Config{Gateway: gateway, Bandit: bandit, Store: store, SaveEvery: 1})
	if err != nil {
		t.Fatalf("new learner: %v", err)
	}
	defer learner.Close()

	stream, err := learner.Stream(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("stream learner: %v", err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("first recv: %v", err)
	}
	done, err := stream.Recv()
	if err != nil {
		t.Fatalf("done recv: %v", err)
	}
	learner.Flush()

	if gateway.streamCalls != 1 {
		t.Fatalf("stream calls got %d", gateway.streamCalls)
	}
	if first.Delta != "hello" || !done.Done {
		t.Fatalf("chunks got %+v %+v", first, done)
	}
	if len(store.saves) == 0 {
		t.Fatal("stream completion should save learned state")
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
