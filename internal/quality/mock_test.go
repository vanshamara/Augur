package quality

import (
	"context"
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

func TestMockScorerIsDeterministic(t *testing.T) {
	scorer := NewMockScorer(MockConfig{
		Seed: 7,
		BackendQuality: map[core.BackendID]float64{
			"a": 0.8,
		},
		Noise: 0.1,
	})
	req := request("req-1")
	resp := response("req-1", "a", "answer")

	first, err := scorer.Score(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	second, err := scorer.Score(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if first != second {
		t.Fatalf("mock scorer should be deterministic, got %+v and %+v", first, second)
	}
}

func TestMockScorerClampsErrorsToZero(t *testing.T) {
	scorer := NewMockScorer(MockConfig{
		BackendQuality: map[core.BackendID]float64{"a": 0.9},
	})
	resp := response("req-1", "a", "answer")
	resp.Errored = true

	result, err := scorer.Score(context.Background(), request("req-1"), resp)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if result.Score != 0 {
		t.Fatalf("errored response should score zero, got %v", result.Score)
	}
}

func request(id string) core.Request {
	return core.Request{
		ID:     id,
		Prompt: "hello",
		Features: core.Features{
			Type: core.Chat,
		},
	}
}

func response(requestID string, backend core.BackendID, output string) core.Response {
	return core.Response{
		RequestID:  requestID,
		Backend:    backend,
		OutputText: output,
	}
}
