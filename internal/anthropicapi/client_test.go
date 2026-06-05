package anthropicapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMessagesSendsAnthropicRequest(t *testing.T) {
	var gotPath string
	var gotKey string
	var gotVersion string
	var gotBody messagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"content":[{"type":"text","text":"hello there"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	result, err := client.Messages(context.Background(), MessageRequest{
		Model:     "claude-test",
		MaxTokens: 256,
		System:    "be brief",
		Messages:  []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("messages: %v", err)
	}

	if gotPath != "/v1/messages" {
		t.Fatalf("path got %q", gotPath)
	}
	if gotKey != "test-key" || gotVersion == "" {
		t.Fatalf("auth headers got key=%q version=%q", gotKey, gotVersion)
	}
	if gotBody.MaxTokens != 256 || gotBody.System != "be brief" {
		t.Fatalf("request body got %+v", gotBody)
	}
	if result.Text != "hello there" || result.InputTokens != 7 || result.OutputTokens != 3 {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestMessagesReturnsAPIErrorWithStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Messages(context.Background(), MessageRequest{
		Model:     "claude-test",
		MaxTokens: 16,
		Messages:  []Message{{Role: "user", Content: "hi"}},
	})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected an APIError, got %T %v", err, err)
	}
	if apiErr.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status got %d", apiErr.StatusCode())
	}
}
