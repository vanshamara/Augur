package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatCompletionSendsOpenAICompatibleRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	result, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path got %s", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("authorization header got %q", gotAuth)
	}
	if gotBody.Model != "test-model" || gotBody.Messages[0].Content != "hi" {
		t.Fatalf("unexpected request body %+v", gotBody)
	}
	if result.Content != "hello" || result.PromptTokens != 3 || result.CompletionTokens != 2 {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestClientReadsAPIKeyFromEnv(t *testing.T) {
	t.Setenv("AUGUR_TEST_OPENAI_KEY", "env-key")
	client, err := New(Config{APIKeyEnv: "AUGUR_TEST_OPENAI_KEY"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if client.apiKey != "env-key" {
		t.Fatal("client should read api key from env")
	}
}

func TestChatCompletionReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected api error")
	}
}
