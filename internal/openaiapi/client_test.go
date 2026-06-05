package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientWorksWithoutAPIKey(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"local"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL + "/v1", APIKeyEnv: "AUGUR_UNSET_KEY_ENV", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client without a key should not fail: %v", err)
	}
	if _, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "local-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("keyless client should send no Authorization header, got %q", gotAuth)
	}
}

func TestEmbeddingsSendsOpenAICompatibleRequest(t *testing.T) {
	var gotPath string
	var gotBody EmbeddingRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"index":1,"embedding":[0.3,0.4]},{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":5}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	result, err := client.Embeddings(context.Background(), EmbeddingRequest{
		Model: "embed-model",
		Input: []string{"first", "second"},
	})
	if err != nil {
		t.Fatalf("embeddings: %v", err)
	}

	if gotPath != "/v1/embeddings" {
		t.Fatalf("path got %s", gotPath)
	}
	if gotBody.Model != "embed-model" || len(gotBody.Input) != 2 {
		t.Fatalf("unexpected request body %+v", gotBody)
	}
	if result.PromptTokens != 5 || len(result.Vectors) != 2 {
		t.Fatalf("unexpected result %+v", result)
	}
	if result.Vectors[0][0] != 0.1 || result.Vectors[1][0] != 0.3 {
		t.Fatalf("vectors not ordered by index: %+v", result.Vectors)
	}
}

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
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode() != http.StatusBadRequest {
		t.Fatalf("api error got %v", err)
	}
}

func TestChatCompletionStreamSendsOpenAICompatibleRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotAccept string
	var gotBody ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	stream, err := client.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat completion stream: %v", err)
	}
	defer stream.Close()

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("first recv: %v", err)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatalf("second recv: %v", err)
	}
	done, err := stream.Recv()
	if err != nil {
		t.Fatalf("done recv: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path got %s", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("authorization header got %q", gotAuth)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("accept header got %q", gotAccept)
	}
	if !gotBody.Stream {
		t.Fatal("stream flag should be true")
	}
	if first.Content != "hel" || second.Content != "lo" || !done.Done {
		t.Fatalf("chunks got %+v %+v %+v", first, second, done)
	}
}

func TestChatCompletionStreamReturnsEOFAfterDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	stream, err := client.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat completion stream: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv done: %v", err)
	}
	if !chunk.Done {
		t.Fatalf("done chunk got %+v", chunk)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("second recv error got %v", err)
	}
}

func TestChatCompletionStreamReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected api error")
	}
}

func TestHealthCheckUsesConfiguredPath(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.HealthCheck(context.Background(), "/healthz"); err != nil {
		t.Fatalf("health check: %v", err)
	}
	if gotPath != "/v1/healthz" {
		t.Fatalf("path got %s", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("authorization got %q", gotAuth)
	}
}
