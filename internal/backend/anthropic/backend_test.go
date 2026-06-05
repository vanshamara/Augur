package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vanshamara/Augur/internal/anthropicapi"
	"github.com/vanshamara/Augur/internal/core"
)

func TestBackendCallsAnthropicMessages(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"content":[{"type":"text","text":"answer"}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer server.Close()

	client, err := anthropicapi.New(anthropicapi.Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	backend, err := New(Config{
		ID:                 "claude",
		Model:              "claude-test",
		Client:             client,
		InputCostPerToken:  0.01,
		OutputCostPerToken: 0.02,
	})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	resp, err := backend.Call(context.Background(), core.Request{
		ID: "req-1",
		Messages: []core.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("call backend: %v", err)
	}

	if gotPath != "/v1/messages" {
		t.Fatalf("path got %q", gotPath)
	}
	if gotBody["system"] != "be brief" {
		t.Fatalf("system not lifted out of messages: %v", gotBody["system"])
	}
	if messages, ok := gotBody["messages"].([]any); !ok || len(messages) != 1 {
		t.Fatalf("messages should exclude the system role: %v", gotBody["messages"])
	}
	if resp.OutputText != "answer" || resp.OutputTokens != 5 {
		t.Fatalf("unexpected response %+v", resp)
	}
	if resp.CostUSD != 0.2 {
		t.Fatalf("cost got %v, want 0.2", resp.CostUSD)
	}
}

func TestBackendRejectsEmbeddingRequests(t *testing.T) {
	client, err := anthropicapi.New(anthropicapi.Config{BaseURL: "http://example.test", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	backend, err := New(Config{ID: "claude", Model: "claude-test", Client: client})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	resp, err := backend.Call(context.Background(), core.Request{
		ID:       "req-embed",
		Features: core.Features{Type: core.Embedding},
	})
	if err == nil {
		t.Fatal("anthropic backend should reject embedding requests")
	}
	if !resp.Errored {
		t.Fatal("response should be marked errored")
	}
}
