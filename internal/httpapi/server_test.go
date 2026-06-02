package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
)

type fakeGateway struct {
	req  core.Request
	resp core.Response
	err  error
}

func (g *fakeGateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	g.req = req
	return g.resp, g.err
}

func TestChatCompletionsRoutesThroughGateway(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:  "req-1",
			Backend:    "fast",
			OutputText: "hello back",
			Outcome: core.Outcome{
				OutputTokens: 7,
			},
		},
	}
	server := testServer(t, gateway)
	body := `{
		"model":"augur-chat",
		"messages":[
			{"role":"system","content":"be brief"},
			{"role":"user","content":"hello"}
		],
		"max_completion_tokens":64,
		"temperature":0.2
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Request-ID", "req-1")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Augur-Backend"); got != "fast" {
		t.Fatalf("backend header got %q", got)
	}
	if gateway.req.ID != "req-1" {
		t.Fatalf("request id got %q", gateway.req.ID)
	}
	if gateway.req.Messages[0].Role != "system" || gateway.req.Messages[1].Content != "hello" {
		t.Fatalf("messages were not preserved: %+v", gateway.req.Messages)
	}
	if gateway.req.MaxCompletionTokens != 64 {
		t.Fatalf("max completion tokens got %d", gateway.req.MaxCompletionTokens)
	}
	if gateway.req.Temperature == nil || *gateway.req.Temperature != 0.2 {
		t.Fatalf("temperature got %v", gateway.req.Temperature)
	}

	var got chatCompletionResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != "chatcmpl-test" || got.Object != "chat.completion" || got.Model != "augur-chat" {
		t.Fatalf("unexpected response metadata %+v", got)
	}
	if got.Choices[0].Message.Role != "assistant" || got.Choices[0].Message.Content != "hello back" {
		t.Fatalf("unexpected choice %+v", got.Choices[0])
	}
	if got.Usage.CompletionTokens != 7 || got.Usage.TotalTokens <= got.Usage.CompletionTokens {
		t.Fatalf("unexpected usage %+v", got.Usage)
	}
}

func TestChatCompletionsRejectsStreaming(t *testing.T) {
	server := testServer(t, &fakeGateway{})
	body := `{"model":"augur-chat","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsMapsGatewayErrors(t *testing.T) {
	server := testServer(t, &fakeGateway{err: dataplane.ErrLoadShed})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsMapsUnknownErrors(t *testing.T) {
	server := testServer(t, &fakeGateway{err: errors.New("backend failed")})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHealth(t *testing.T) {
	server := testServer(t, &fakeGateway{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d", rec.Code)
	}
}

func testServer(t *testing.T, gateway Gateway) *Server {
	t.Helper()
	server, err := New(Config{
		Gateway: gateway,
		Now: func() time.Time {
			return time.Unix(123, 0)
		},
		NewID: func() string {
			return "chatcmpl-test"
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}
