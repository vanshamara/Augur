package openai

import (
	"context"
	"encoding/json"
	"io"
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

func TestBackendPassesMessagesAndOptions(t *testing.T) {
	var gotBody openaiapi.ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer server.Close()

	client, err := openaiapi.New(openaiapi.Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	backend, err := New(Config{ID: "openai", Model: "test-model", Client: client, MaxCompletionTokens: 16})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	temperature := 0.2

	_, err = backend.Call(context.Background(), core.Request{
		ID: "req-1",
		Messages: []core.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
		},
		MaxCompletionTokens: 32,
		Temperature:         &temperature,
	})
	if err != nil {
		t.Fatalf("call backend: %v", err)
	}

	if len(gotBody.Messages) != 2 || gotBody.Messages[0].Role != "system" || gotBody.Messages[1].Content != "hello" {
		t.Fatalf("messages got %+v", gotBody.Messages)
	}
	if gotBody.MaxTokens != 32 {
		t.Fatalf("max tokens got %d", gotBody.MaxTokens)
	}
	if gotBody.Temperature == nil || *gotBody.Temperature != 0.2 {
		t.Fatalf("temperature got %v", gotBody.Temperature)
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

func TestBackendStreamsOpenAICompatibleChat(t *testing.T) {
	var gotBody openaiapi.ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := openaiapi.New(openaiapi.Config{BaseURL: server.URL, APIKey: "test-key", Client: server.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	backend, err := New(Config{
		ID:                  "openai",
		Model:               "test-model",
		Client:              client,
		InputCostPerToken:   0.01,
		OutputCostPerToken:  0.02,
		MaxCompletionTokens: 16,
	})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	stream, err := backend.Stream(context.Background(), core.Request{
		ID: "req-1",
		Messages: []core.Message{
			{Role: "user", Content: "hello"},
		},
		Features: core.Features{
			PromptTokens: 3,
		},
	})
	if err != nil {
		t.Fatalf("stream backend: %v", err)
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

	if !gotBody.Stream {
		t.Fatal("stream flag should be true")
	}
	if gotBody.MaxTokens != 16 {
		t.Fatalf("max tokens got %d", gotBody.MaxTokens)
	}
	if first.RequestID != "req-1" || first.Backend != "openai" || first.Delta != "hel" {
		t.Fatalf("first chunk got %+v", first)
	}
	if second.Delta != "lo" {
		t.Fatalf("second chunk got %+v", second)
	}
	if !done.Done {
		t.Fatalf("done chunk got %+v", done)
	}
	if done.OutputTokens <= 0 {
		t.Fatalf("output tokens got %d", done.OutputTokens)
	}
	if done.CostUSD <= 0 {
		t.Fatalf("cost got %v", done.CostUSD)
	}
}

func TestBackendStreamReturnsEOFAfterDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: [DONE]\n\n"))
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
	stream, err := backend.Stream(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("stream backend: %v", err)
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

func TestBackendStreamReturnsAPIError(t *testing.T) {
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

	_, err = backend.Stream(context.Background(), core.Request{ID: "req-1", Prompt: "hello"})
	if err == nil {
		t.Fatal("expected api error")
	}
}
