package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
)

type fakeGateway struct {
	calls         int
	streamCalls   int
	req           core.Request
	streamReq     core.Request
	resp          core.Response
	err           error
	stream        core.Stream
	streamErr     error
	streamFactory func(context.Context, core.Request) (core.Stream, error)
}

func (g *fakeGateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	g.calls++
	g.req = req
	return g.resp, g.err
}

func (g *fakeGateway) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	g.streamCalls++
	g.streamReq = req
	if g.streamFactory != nil {
		return g.streamFactory(ctx, req)
	}
	return g.stream, g.streamErr
}

type fakeStream struct {
	chunks []core.StreamChunk
	index  int
	closed bool
}

func (s *fakeStream) Recv() (core.StreamChunk, error) {
	if s.index >= len(s.chunks) {
		return core.StreamChunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

type contextStream struct {
	ctx context.Context
}

func (s contextStream) Recv() (core.StreamChunk, error) {
	<-s.ctx.Done()
	return core.StreamChunk{}, s.ctx.Err()
}

func (s contextStream) Close() error {
	return nil
}

type fakeAttemptError struct{}

func (fakeAttemptError) Error() string {
	return "all fallback backends failed"
}

func (fakeAttemptError) Is(target error) bool {
	return target == dataplane.ErrAllBackendsFailed
}

func (fakeAttemptError) AttemptedBackends() []core.BackendID {
	return []core.BackendID{"primary", "backup"}
}

func (fakeAttemptError) FallbackCount() int {
	return 1
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
				{"role":"user","content":"Give a concise summary of why reliable routing matters for production systems."}
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
	if gateway.req.Messages[0].Role != "system" || gateway.req.Messages[1].Content != "Give a concise summary of why reliable routing matters for production systems." {
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

func TestChatCompletionsWritesRouteHeader(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:  "req-1",
			RouteName:  "chat-route",
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
	if got := rec.Header().Get("X-Augur-Route"); got != "chat-route" {
		t.Fatalf("route header got %q", got)
	}
}

func TestChatCompletionsWritesFallbackHeaders(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:         "req-1",
			RouteName:         "chat-route",
			Backend:           "backup",
			AttemptedBackends: []core.BackendID{"primary", "backup"},
			FallbackCount:     1,
			OutputText:        "answer",
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
	if got := rec.Header().Get("X-Augur-Backend"); got != "backup" {
		t.Fatalf("backend header got %q", got)
	}
	if got := rec.Header().Get("X-Augur-Fallback-Count"); got != "1" {
		t.Fatalf("fallback count header got %q", got)
	}
	if got := rec.Header().Get("X-Augur-Attempted-Backends"); got != "primary,backup" {
		t.Fatalf("attempted backends header got %q", got)
	}
}

func TestChatCompletionsWritesCanaryHeaders(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:      "req-1",
			Backend:        "candidate",
			CanaryMode:     "live",
			CanaryBackend:  "candidate",
			OutputText:     "answer",
			CanaryRollback: "error_rate",
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
	if got := rec.Header().Get("X-Augur-Canary"); got != "live" {
		t.Fatalf("canary header got %q", got)
	}
	if got := rec.Header().Get("X-Augur-Canary-Backend"); got != "candidate" {
		t.Fatalf("canary backend header got %q", got)
	}
	if got := rec.Header().Get("X-Augur-Canary-Rollback"); got != "error_rate" {
		t.Fatalf("canary rollback header got %q", got)
	}
}

func TestChatCompletionsUsesDefaultOptions(t *testing.T) {
	temperature := 0.4
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServerWithDefaults(t, gateway, RequestDefaults{
		MaxCompletionTokens: 99,
		Temperature:         &temperature,
	})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"Give a concise summary of why reliable routing matters for production systems."}]}`
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

func TestChatCompletionsUsesUserIDHints(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServer(t, gateway)
	body := `{
		"model":"augur-chat",
		"metadata":{"augur_user_id":"metadata-user"},
		"messages":[{"role":"user","content":"hello"}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Augur-User-ID", "header-user")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.UserID != "header-user" {
		t.Fatalf("user id got %q", gateway.req.UserID)
	}
}

func TestChatCompletionsUsesDefaultTenant(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServer(t, gateway)
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"Give a concise summary of why reliable routing matters for production systems."}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.TenantID != "default" {
		t.Fatalf("tenant got %q", gateway.req.TenantID)
	}
}

func TestChatCompletionsUsesTenantHeader(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServer(t, gateway)
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"Give a concise summary of why reliable routing matters for production systems."}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Augur-Tenant", "tenant-a")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.TenantID != "tenant-a" {
		t.Fatalf("tenant got %q", gateway.req.TenantID)
	}
}

func TestChatCompletionsAppliesTenantPolicyDefaults(t *testing.T) {
	temperature := 0.1
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server, err := New(Config{
		Gateway: gateway,
		Defaults: RequestDefaults{
			LatencyBudgetMs:     1200,
			CostBudgetUSD:       0.01,
			MaxCompletionTokens: 64,
			UserTier:            "standard",
		},
		TenantDefaults: map[string]RequestDefaults{
			"premium": {
				LatencyBudgetMs:     800,
				CostBudgetUSD:       0.03,
				MaxCompletionTokens: 256,
				Temperature:         &temperature,
				UserTier:            "premium",
			},
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
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"Give a concise summary of why reliable routing matters for production systems."}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Augur-Tenant", "premium")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.Features.LatencyBudgetMs != 800 || gateway.req.Features.CostBudget != 0.03 {
		t.Fatalf("tenant budgets got %+v", gateway.req.Features)
	}
	if gateway.req.MaxCompletionTokens != 256 || gateway.req.Features.UserTier != "premium" {
		t.Fatalf("tenant defaults got request %+v features %+v", gateway.req, gateway.req.Features)
	}
	if gateway.req.Temperature == nil || *gateway.req.Temperature != 0.1 {
		t.Fatalf("tenant temperature got %v", gateway.req.Temperature)
	}
}

func TestChatCompletionsUsesRequestMetadata(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "strong", OutputText: "answer"}}
	server := testServerWithDefaults(t, gateway, RequestDefaults{
		LatencyBudgetMs: 1200,
		CostBudgetUSD:   0.01,
		UserTier:        "standard",
	})
	body := `{
		"model":"augur-chat",
		"metadata":{
			"augur_request_type":"reasoning",
			"augur_user_tier":"premium",
			"augur_latency_budget_ms":"2400",
			"augur_cost_budget_usd":"0.05",
			"augur_prompt_tokens":"1234"
		},
		"messages":[{"role":"user","content":"solve a hard problem"}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.Features.Type != core.Reasoning {
		t.Fatalf("request type got %q", gateway.req.Features.Type)
	}
	if gateway.req.Features.UserTier != "premium" {
		t.Fatalf("user tier got %q", gateway.req.Features.UserTier)
	}
	if gateway.req.Features.LatencyBudgetMs != 2400 || gateway.req.Features.CostBudget != 0.05 {
		t.Fatalf("request budgets got %+v", gateway.req.Features)
	}
	if gateway.req.Features.PromptTokens != 1234 {
		t.Fatalf("prompt tokens got %d", gateway.req.Features.PromptTokens)
	}
}

func TestChatCompletionsHeadersOverrideRequestMetadata(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServer(t, gateway)
	body := `{
		"model":"augur-chat",
		"metadata":{
			"augur_request_type":"chat",
			"augur_user_tier":"standard",
			"augur_latency_budget_ms":"1800",
			"augur_cost_budget_usd":"0.02",
			"augur_prompt_tokens":"200"
		},
		"messages":[{"role":"user","content":"ship this feature"}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Augur-Request-Type", "coding")
	req.Header.Set("X-Augur-User-Tier", "enterprise")
	req.Header.Set("X-Augur-Latency-Budget-Ms", "900")
	req.Header.Set("X-Augur-Cost-Budget-USD", "0.10")
	req.Header.Set("X-Augur-Prompt-Tokens", "77")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.Features.Type != core.Coding {
		t.Fatalf("request type got %q", gateway.req.Features.Type)
	}
	if gateway.req.Features.UserTier != "enterprise" {
		t.Fatalf("user tier got %q", gateway.req.Features.UserTier)
	}
	if gateway.req.Features.LatencyBudgetMs != 900 || gateway.req.Features.CostBudget != 0.10 {
		t.Fatalf("request budgets got %+v", gateway.req.Features)
	}
	if gateway.req.Features.PromptTokens != 77 {
		t.Fatalf("prompt tokens got %d", gateway.req.Features.PromptTokens)
	}
}

func TestChatCompletionsRejectsInvalidRequestMetadata(t *testing.T) {
	server := testServer(t, &fakeGateway{})
	body := `{
		"model":"augur-chat",
		"metadata":{"augur_request_type":"magic"},
		"messages":[{"role":"user","content":"hello"}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsRejectsInvalidPromptTokenHeader(t *testing.T) {
	server := testServer(t, &fakeGateway{})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Augur-Prompt-Tokens", "0")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsInfersReasoningWithoutHints(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "strong", OutputText: "answer"}}
	server := testServerWithDefaults(t, gateway, RequestDefaults{
		LatencyBudgetMs: 1200,
		CostBudgetUSD:   0.01,
	})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"Solve this carefully: if 8 workers finish 24 tasks in 6 hours, how many tasks can 12 workers finish in 9 hours?"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.Features.Type != core.Reasoning {
		t.Fatalf("request type got %q", gateway.req.Features.Type)
	}
	if gateway.req.Features.CostBudget < reasoningCostBudgetUSD || gateway.req.Features.LatencyBudgetMs < reasoningLatencyBudgetMs {
		t.Fatalf("reasoning features got %+v", gateway.req.Features)
	}
}

func TestChatCompletionsInfersSimpleWithoutHints(t *testing.T) {
	gateway := &fakeGateway{resp: core.Response{RequestID: "req-1", Backend: "fast", OutputText: "answer"}}
	server := testServerWithDefaults(t, gateway, RequestDefaults{
		LatencyBudgetMs: 1200,
		CostBudgetUSD:   0.01,
	})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if gateway.req.Features.Type != core.Chat {
		t.Fatalf("request type got %q", gateway.req.Features.Type)
	}
	if gateway.req.Features.CostBudget != simpleCostBudgetUSD || gateway.req.Features.LatencyBudgetMs != simpleLatencyBudgetMs {
		t.Fatalf("simple features got %+v", gateway.req.Features)
	}
}

func TestChatCompletionsStreamsSSE(t *testing.T) {
	gateway := &fakeGateway{
		stream: &fakeStream{
			chunks: []core.StreamChunk{
				{Delta: "hel"},
				{Delta: "lo"},
				{Done: true},
			},
		},
	}
	server := testServer(t, gateway)
	body := `{"model":"augur-chat","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type got %q", rec.Header().Get("Content-Type"))
	}
	bodyText := rec.Body.String()
	if !strings.Contains(bodyText, `"object":"chat.completion.chunk"`) {
		t.Fatalf("stream body missing chunk object: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"content":"hel"`) || !strings.Contains(bodyText, `"content":"lo"`) {
		t.Fatalf("stream body missing deltas: %s", bodyText)
	}
	if !strings.Contains(bodyText, "data: [DONE]") {
		t.Fatalf("stream body missing done marker: %s", bodyText)
	}
	if gateway.streamReq.ID == "" || gateway.streamReq.Messages[0].Content != "hello" {
		t.Fatalf("stream request got %+v", gateway.streamReq)
	}
}

func TestChatCompletionsMapsStreamGatewayErrors(t *testing.T) {
	server := testServer(t, &fakeGateway{streamErr: dataplane.ErrNoCandidates})
	body := `{"model":"augur-chat","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsStopsStreamingOnCancellation(t *testing.T) {
	started := make(chan struct{})
	gateway := &fakeGateway{
		streamFactory: func(ctx context.Context, req core.Request) (core.Stream, error) {
			close(started)
			return contextStream{ctx: ctx}, nil
		},
	}
	server := testServer(t, gateway)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"augur-chat","stream":true,"messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		server.ServeHTTP(rec, req)
		close(done)
	}()
	<-started
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
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

func TestChatCompletionsMapsAllFallbacksFailed(t *testing.T) {
	server := testServer(t, &fakeGateway{err: fakeAttemptError{}})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Augur-Fallback-Count"); got != "1" {
		t.Fatalf("fallback count header got %q", got)
	}
	if got := rec.Header().Get("X-Augur-Attempted-Backends"); got != "primary,backup" {
		t.Fatalf("attempted backends header got %q", got)
	}
}

func TestChatCompletionsMapsCompatibilityErrors(t *testing.T) {
	err := dataplane.ErrNoCompatibleCandidates
	server := testServer(t, &fakeGateway{err: err})
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "request type") {
		t.Fatalf("body did not mention request type: %s", rec.Body.String())
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

func TestBackendDebugReturnsStatus(t *testing.T) {
	server, err := New(Config{
		Gateway: &fakeGateway{},
		BackendStatus: func() []dataplane.BackendStatus {
			return []dataplane.BackendStatus{
				{
					ID:                  "fast",
					Healthy:             true,
					CircuitMode:         "closed",
					ConcurrencyLimit:    8,
					Samples:             4,
					P95LatencyMs:        120,
					ErrorRate:           0.25,
					BackendTimeoutMs:    500,
					HealthError:         "",
					ConsecutiveFailures: 0,
				},
			}
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/debug/backends", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Backends []dataplane.BackendStatus `json:"backends"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Backends) != 1 || body.Backends[0].ID != "fast" || body.Backends[0].CircuitMode != "closed" {
		t.Fatalf("debug body got %+v", body)
	}
}

func TestDecisionDebugReturnsRecordByRequestID(t *testing.T) {
	record := dataplane.RouteDecisionRecord{
		RequestID: "req-1",
		RouteName: "default",
		Selected:  "fast",
		Excluded: []dataplane.ExclusionRecord{
			{Backend: "slow", Stage: "budget", Reason: "estimated cost over budget"},
		},
	}
	server, err := New(Config{
		Gateway: &fakeGateway{},
		Decision: func(requestID string) (dataplane.RouteDecisionRecord, bool) {
			if requestID == "req-1" {
				return record, true
			}
			return dataplane.RouteDecisionRecord{}, false
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/debug/decisions?request_id=req-1", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	var got dataplane.RouteDecisionRecord
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.RequestID != "req-1" || got.Selected != "fast" {
		t.Fatalf("decision body got %+v", got)
	}
	if len(got.Excluded) != 1 || got.Excluded[0].Backend != "slow" {
		t.Fatalf("exclusions got %+v", got.Excluded)
	}
}

func TestDecisionDebugReturnsNotFoundForUnknownRequest(t *testing.T) {
	server, err := New(Config{
		Gateway: &fakeGateway{},
		Decision: func(string) (dataplane.RouteDecisionRecord, bool) {
			return dataplane.RouteDecisionRecord{}, false
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/debug/decisions?request_id=missing", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestDecisionDebugRequiresAuthWhenEnabled(t *testing.T) {
	server := testServerWithAuth(t, &fakeGateway{}, []string{"client-key"})
	req := httptest.NewRequest(http.MethodGet, "/debug/decisions", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestBackendDebugRequiresAuthWhenEnabled(t *testing.T) {
	server := testServerWithAuth(t, &fakeGateway{}, []string{"client-key"})
	req := httptest.NewRequest(http.MethodGet, "/debug/backends", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsWritesCostHeaders(t *testing.T) {
	gateway := &fakeGateway{
		resp: core.Response{
			RequestID:        "req-1",
			Backend:          "fast",
			OutputText:       "hello",
			EstimatedCostUSD: 0.004,
			Outcome: core.Outcome{
				OutputTokens: 5,
				CostUSD:      0.0031,
			},
		},
	}
	server := testServer(t, gateway)
	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hello there"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Augur-Estimated-Cost-USD"); got != "0.004" {
		t.Fatalf("estimated cost header got %q", got)
	}
	if got := rec.Header().Get("X-Augur-Cost-USD"); got != "0.0031" {
		t.Fatalf("realized cost header got %q", got)
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
