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
	calls int
	req   core.Request
	resp  core.Response
	err   error
}

func (g *fakeGateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	g.calls++
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
	if gateway.req.Features.LatencyBudgetMs != 1200 || gateway.req.Features.CostBudget != 0.01 {
		t.Fatalf("budgets got %+v", gateway.req.Features)
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

func TestChatCompletionsUsesDefaultOptions(t *testing.T) {
	temperature := 0.4
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServerWithDefaults(t, gateway, RequestDefaults{
		MaxCompletionTokens: 99,
		Temperature:         &temperature,
	})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.MaxCompletionTokens != 99 {
		t.Fatalf("max completion tokens got %d", gateway.req.MaxCompletionTokens)
	}
	if gateway.req.Temperature == nil || *gateway.req.Temperature != 0.4 {
		t.Fatalf("temperature got %v", gateway.req.Temperature)
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

func TestChatCompletionsRejectsLargeBody(t *testing.T) {
	server, err := New(Config{
		Gateway:      &fakeGateway{},
		MaxBodyBytes: 16,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsAllowsMissingAPIKeyWhenAuthDisabled(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:  "req-1",
			Backend:    "fast",
			OutputText: "answer",
		},
	}
	server := testServer(t, gateway)
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.calls != 1 {
		t.Fatalf("gateway calls got %d", gateway.calls)
	}
}

func TestChatCompletionsRejectsMissingAPIKey(t *testing.T) {
	gateway := &fakeGateway{}
	server := testServerWithAuth(t, gateway, []string{"client-key"})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.calls != 0 {
		t.Fatalf("gateway calls got %d", gateway.calls)
	}
}

func TestChatCompletionsRejectsBadAPIKey(t *testing.T) {
	gateway := &fakeGateway{}
	server := testServerWithAuth(t, gateway, []string{"client-key"})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.calls != 0 {
		t.Fatalf("gateway calls got %d", gateway.calls)
	}
}

func TestChatCompletionsAcceptsBearerAPIKey(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:  "req-1",
			Backend:    "fast",
			OutputText: "answer",
		},
	}
	server := testServerWithAuth(t, gateway, []string{"client-key"})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer client-key")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.calls != 1 {
		t.Fatalf("gateway calls got %d", gateway.calls)
	}
}

func TestChatCompletionsAcceptsHeaderAPIKey(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:  "req-1",
			Backend:    "fast",
			OutputText: "answer",
		},
	}
	server := testServerWithAuth(t, gateway, []string{"client-key"})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Augur-API-Key", "client-key")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.calls != 1 {
		t.Fatalf("gateway calls got %d", gateway.calls)
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

func TestHealthStaysPublicWhenAuthEnabled(t *testing.T) {
	server := testServerWithAuth(t, &fakeGateway{}, []string{"client-key"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestReady(t *testing.T) {
	server := testServer(t, &fakeGateway{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestReadyCanFail(t *testing.T) {
	server, err := New(Config{
		Gateway: &fakeGateway{},
		Ready: func(ctx context.Context) bool {
			return false
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func testServer(t *testing.T, gateway Gateway) *Server {
	t.Helper()
	return testServerWithDefaults(t, gateway, RequestDefaults{
		LatencyBudgetMs: 1200,
		CostBudgetUSD:   0.01,
	})
}

func testServerWithDefaults(t *testing.T, gateway Gateway, defaults RequestDefaults) *Server {
	t.Helper()
	server, err := New(Config{
		Gateway:  gateway,
		Defaults: defaults,
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

func testServerWithAuth(t *testing.T, gateway Gateway, keys []string) *Server {
	t.Helper()
	server, err := New(Config{
		Gateway:  gateway,
		AuthKeys: keys,
		Defaults: RequestDefaults{
			LatencyBudgetMs: 1200,
			CostBudgetUSD:   0.01,
		},
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
