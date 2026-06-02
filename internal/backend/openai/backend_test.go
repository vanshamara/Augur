package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/openaiapi"
)

func TestBackendCallsOpenAICompatibleChat(t *testing.T) {
	var gotPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body openaiapi.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotPrompt = body.Messages[0].Content
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer server.Close()

	client, err := openaiapi.New(openaiapi.Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	backend, err := New(Config{
		ID:                 "openai",
		Model:              "test-model",
		Client:             client,
		InputCostPerToken:  0.01,
		OutputCostPerToken: 0.02,
	})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	resp, err := backend.Call(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("call backend: %v", err)
	}

	if gotPrompt != "hello" {
		t.Fatalf("prompt got %q", gotPrompt)
	}
	if resp.RequestID != "req-1" || resp.Backend != "openai" || resp.OutputText != "answer" {
		t.Fatalf("unexpected response %+v", resp)
	}
	if resp.OutputTokens != 5 {
		t.Fatalf("output tokens got %d", resp.OutputTokens)
	}
	if resp.CostUSD != 0.20 {
		t.Fatalf("cost got %v", resp.CostUSD)
	}
	if resp.LatencyMs < 0 {
		t.Fatalf("latency should not be negative, got %v", resp.LatencyMs)
	}
}

func TestBackendMarksAPIErrorAsErrored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"failed"}}`))
	}))
	defer server.Close()

	client, err := openaiapi.New(openaiapi.Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	backend, err := New(Config{ID: "openai", Model: "test-model", Client: client})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	resp, err := backend.Call(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err == nil {
		t.Fatal("expected api error")
	}
	if !resp.Errored {
		t.Fatal("api errors should mark the response as errored")
	}
}
