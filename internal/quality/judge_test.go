package quality

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vanshamara/Augur/internal/openaiapi"
)

func TestJudgeScorerRequestsStructuredScore(t *testing.T) {
	var gotBody openaiapi.ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"score\":0.91,\"reason\":\"good\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer server.Close()

	client, err := openaiapi.New(openaiapi.Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	scorer, err := NewJudgeScorer(JudgeConfig{Model: "judge-model", Client: client, SampleRate: 1})
	if err != nil {
		t.Fatalf("new judge: %v", err)
	}

	result, err := scorer.Score(context.Background(), request("req-1"), response("req-1", "a", "answer"))
	if err != nil {
		t.Fatalf("score: %v", err)
	}

	if result.Score != 0.91 || result.Reason != "good" {
		t.Fatalf("unexpected result %+v", result)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_schema" {
		t.Fatalf("judge should request structured JSON, got %+v", gotBody.ResponseFormat)
	}
	if gotBody.Messages[0].Role != "system" {
		t.Fatalf("first judge message should be system instructions, got %s", gotBody.Messages[0].Role)
	}
}

func TestJudgeSamplingIsDeterministic(t *testing.T) {
	client, err := openaiapi.New(openaiapi.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	scorer, err := NewJudgeScorer(JudgeConfig{Model: "judge-model", Client: client, SampleRate: 0.5, Seed: 9})
	if err != nil {
		t.Fatalf("new judge: %v", err)
	}
	req := request("req-1")
	resp := response("req-1", "a", "answer")

	first := scorer.ShouldScore(req, resp)
	second := scorer.ShouldScore(req, resp)
	if first != second {
		t.Fatal("sampling should be stable for the same request and backend")
	}
}

func TestJudgeScorerRequiresModelAndClient(t *testing.T) {
	if _, err := NewJudgeScorer(JudgeConfig{}); err == nil {
		t.Fatal("judge scorer should require model and client")
	}
}
