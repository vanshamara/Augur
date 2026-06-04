package dataplane

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/observability"
	"github.com/vanshamara/Augur/internal/router"
)

type fakeBackend struct {
	id       core.BackendID
	delay    time.Duration
	response core.Response
	err      error
	calls    atomic.Int64
	cancels  atomic.Int64
}

func (f *fakeBackend) ID() core.BackendID {
	return f.id
}

func (f *fakeBackend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			f.cancels.Add(1)
			return core.Response{}, ctx.Err()
		}
	}

	resp := f.response
	if resp.Backend == "" {
		resp.Backend = f.id
	}
	return resp, f.err
}

type fakeStreamBackend struct {
	*fakeBackend
	chunks    []core.StreamChunk
	streamErr error
	recvErr   error
}

func (f *fakeStreamBackend) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	f.calls.Add(1)
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	return &fakeStream{chunks: append([]core.StreamChunk(nil), f.chunks...), recvErr: f.recvErr}, nil
}

type fakeStream struct {
	chunks  []core.StreamChunk
	recvErr error
	index   int
	closed  bool
}

func (s *fakeStream) Recv() (core.StreamChunk, error) {
	if s.index >= len(s.chunks) {
		if s.recvErr != nil {
			return core.StreamChunk{}, s.recvErr
		}
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

type fakeStatusError struct {
	status int
}

func (e fakeStatusError) Error() string {
	return "upstream status " + itoa(e.status)
}

func (e fakeStatusError) StatusCode() int {
	return e.status
}

type recordingFilter struct {
	filterBase
	name string
	log  *[]string
}

func (r recordingFilter) Name() string {
	return r.name
}

func (r recordingFilter) Apply(req core.Request, candidates []core.BackendID) []core.BackendID {
	*r.log = append(*r.log, r.name)
	return candidates
}

func TestGatewayAppliesFiltersInOrder(t *testing.T) {
	var order []string
	backends := []backend.Backend{instantBackend("a"), instantBackend("b")}
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: backends,
		Filters: []Filter{
			recordingFilter{name: "health", log: &order},
			recordingFilter{name: "circuit", log: &order},
			recordingFilter{name: "concurrency", log: &order},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	want := []string{"health", "circuit", "concurrency"}
	for i, value := range want {
		if order[i] != value {
			t.Fatalf("filter %d got %s want %s", i, order[i], value)
		}
	}
}

func TestHealthFilterRemovesUnhealthyBackend(t *testing.T) {
	backends := []backend.Backend{instantBackend("a"), instantBackend("b")}
	health := NewHealthFilter([]core.BackendID{"a", "b"})
	health.Set("a", false)

	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: backends,
		Filters:  []Filter{health},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "b" {
		t.Fatalf("expected healthy backend b, got %s", resp.Backend)
	}
}

func TestGatewayUsesMatchedRouteCandidates(t *testing.T) {
	chat := instantBackend("chat")
	reasoning := instantBackend("reasoning")
	gateway, err := New(Config{
		Router:   router.NewStatic("reasoning"),
		Backends: []backend.Backend{chat, reasoning},
		Routes: []RouteRule{
			{
				Name:       "chat-route",
				Match:      RouteMatch{TaskTypes: []core.RequestType{core.Chat}},
				Candidates: []core.BackendID{"chat"},
			},
			{
				Name:       "reasoning-route",
				Match:      RouteMatch{TaskTypes: []core.RequestType{core.Reasoning}},
				Candidates: []core.BackendID{"reasoning"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{
		ID: "req",
		Features: core.Features{
			Type: core.Chat,
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "chat" || resp.RouteName != "chat-route" {
		t.Fatalf("route response got %+v", resp)
	}
}

func TestGatewayReturnsNoCandidatesWhenNoRouteMatches(t *testing.T) {
	gateway, err := New(Config{
		Router:   router.NewRoundRobin(),
		Backends: []backend.Backend{instantBackend("chat")},
		Routes: []RouteRule{
			{
				Name:       "reasoning-route",
				Match:      RouteMatch{TaskTypes: []core.RequestType{core.Reasoning}},
				Candidates: []core.BackendID{"chat"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{
		ID: "req",
		Features: core.Features{
			Type: core.Chat,
		},
	})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("no route error got %v", err)
	}
}

func TestGatewayFiltersCandidatesByCapability(t *testing.T) {
	chat := &fakeBackend{id: "chat"}
	reasoning := &fakeBackend{id: "reasoning"}
	gateway, err := New(Config{
		Router:   router.NewStatic("reasoning"),
		Backends: []backend.Backend{chat, reasoning},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"chat", "reasoning"},
			},
		},
		Capabilities: map[core.BackendID][]core.RequestType{
			"chat":      []core.RequestType{core.Chat},
			"reasoning": []core.RequestType{core.Reasoning},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{
		ID: "req",
		Features: core.Features{
			Type: core.Chat,
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "chat" || reasoning.calls.Load() != 0 {
		t.Fatalf("capability-filtered response got %+v reasoning calls %d", resp, reasoning.calls.Load())
	}
}

func TestGatewayRoutesEmbeddingToCapableBackend(t *testing.T) {
	chat := &fakeBackend{id: "chat"}
	embedding := &fakeBackend{id: "embedding"}
	gateway, err := New(Config{
		Router:   router.NewStatic("chat"),
		Backends: []backend.Backend{chat, embedding},
		Capabilities: map[core.BackendID][]core.RequestType{
			"chat":      []core.RequestType{core.Chat},
			"embedding": []core.RequestType{core.Embedding},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{
		ID: "req",
		Features: core.Features{
			Type: core.Embedding,
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "embedding" || chat.calls.Load() != 0 {
		t.Fatalf("embedding response got %+v chat calls %d", resp, chat.calls.Load())
	}
}

func TestGatewayReturnsCompatibilityErrorWhenNoBackendSupportsTask(t *testing.T) {
	chat := &fakeBackend{id: "chat"}
	gateway, err := New(Config{
		Router:   router.NewStatic("chat"),
		Backends: []backend.Backend{chat},
		Capabilities: map[core.BackendID][]core.RequestType{
			"chat": []core.RequestType{core.Chat},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{
		ID: "req",
		Features: core.Features{
			Type: core.Embedding,
		},
	})
	if !errors.Is(err, ErrNoCompatibleCandidates) {
		t.Fatalf("compatibility error got %v", err)
	}
	if chat.calls.Load() != 0 {
		t.Fatalf("incompatible backend was called %d times", chat.calls.Load())
	}
}

func TestGatewayFallsBackOnTimeout(t *testing.T) {
	primary := &fakeBackend{id: "primary", err: context.DeadlineExceeded}
	backup := &fakeBackend{id: "backup"}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "backup" || resp.FallbackCount != 1 {
		t.Fatalf("fallback response got %+v", resp)
	}
	if len(resp.AttemptedBackends) != 2 || resp.AttemptedBackends[0] != "primary" || resp.AttemptedBackends[1] != "backup" {
		t.Fatalf("attempts got %+v", resp.AttemptedBackends)
	}
}

func TestGatewayFallsBackOnServerError(t *testing.T) {
	primary := &fakeBackend{id: "primary", err: fakeStatusError{status: 500}}
	backup := &fakeBackend{id: "backup"}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "backup" || primary.calls.Load() != 1 || backup.calls.Load() != 1 {
		t.Fatalf("fallback calls got resp=%+v primary=%d backup=%d", resp, primary.calls.Load(), backup.calls.Load())
	}
}

func TestGatewayFallsBackOnRateLimit(t *testing.T) {
	primary := &fakeBackend{id: "primary", err: fakeStatusError{status: 429}}
	backup := &fakeBackend{id: "backup"}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "backup" {
		t.Fatalf("fallback response got %+v", resp)
	}
}

func TestGatewayDoesNotFallbackOnClientError(t *testing.T) {
	primary := &fakeBackend{id: "primary", err: fakeStatusError{status: 400}}
	backup := &fakeBackend{id: "backup"}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err == nil {
		t.Fatal("client error should be returned")
	}
	if resp.Backend != "primary" || backup.calls.Load() != 0 {
		t.Fatalf("non-retryable fallback got resp=%+v backup calls=%d", resp, backup.calls.Load())
	}
}

func TestGatewayReturnsAllBackendsFailed(t *testing.T) {
	primary := &fakeBackend{id: "primary", err: fakeStatusError{status: 500}}
	backup := &fakeBackend{id: "backup", err: fakeStatusError{status: 503}}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if !errors.Is(err, ErrAllBackendsFailed) {
		t.Fatalf("all failed error got %v", err)
	}
	if resp.Backend != "backup" || resp.FallbackCount != 1 {
		t.Fatalf("all failed response got %+v", resp)
	}
	if len(resp.AttemptedBackends) != 2 {
		t.Fatalf("attempts got %+v", resp.AttemptedBackends)
	}
}

func TestGatewayStopsFallbackWhenCostBudgetIsSpent(t *testing.T) {
	primary := &fakeBackend{
		id: "primary",
		response: core.Response{
			Outcome: core.Outcome{
				CostUSD: 0.02,
				Errored: true,
			},
		},
	}
	backup := &fakeBackend{id: "backup"}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{
		ID: "req",
		Features: core.Features{
			CostBudget: 0.01,
		},
	})
	if !errors.Is(err, ErrFallbackBudgetExceeded) {
		t.Fatalf("budget error got %v", err)
	}
	if backup.calls.Load() != 0 {
		t.Fatalf("backup should not be called after budget is spent, got %d", backup.calls.Load())
	}
}

func TestGatewayUsesFallbackWhenPrimaryFilteredOut(t *testing.T) {
	primary := &fakeBackend{id: "primary"}
	backup := &fakeBackend{id: "backup"}
	health := NewHealthFilter([]core.BackendID{"primary", "backup"})
	health.Set("primary", false)
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
		Filters: []Filter{health},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "backup" || primary.calls.Load() != 0 {
		t.Fatalf("filtered fallback got resp=%+v primary calls=%d", resp, primary.calls.Load())
	}
}

func TestRouteSelectorMatchesTenantAndTier(t *testing.T) {
	selector := NewRouteSelector([]RouteRule{
		{
			Name: "premium",
			Match: RouteMatch{
				Tenants:   []string{"tenant-a"},
				UserTiers: []string{"Premium"},
			},
			Candidates: []core.BackendID{"strong"},
		},
		{
			Name:       "default",
			Candidates: []core.BackendID{"fast"},
		},
	})

	decision := selector.Select(core.Request{
		TenantID: "tenant-a",
		Features: core.Features{
			UserTier: "premium",
		},
	}, []core.BackendID{"fast", "strong"})

	if decision.Name != "premium" || len(decision.Candidates) != 1 || decision.Candidates[0] != "strong" {
		t.Fatalf("decision got %+v", decision)
	}
}

func TestAdaptiveLimiterShedsAndAdjustsLimit(t *testing.T) {
	limiter := NewAdaptiveLimiter([]core.BackendID{"a"}, LimitConfig{
		InitialLimit:    1,
		MinLimit:        1,
		MaxLimit:        4,
		TargetLatencyMs: 100,
	})

	release, ok := limiter.Acquire(core.Request{ID: "req-1"}, "a")
	if !ok {
		t.Fatal("first request should be admitted")
	}
	if _, ok := limiter.Acquire(core.Request{ID: "req-2"}, "a"); ok {
		t.Fatal("second request should be shed at limit one")
	}
	release()

	limiter.Observe("a", core.Response{Backend: "a", Outcome: core.Outcome{LatencyMs: 50}}, nil)
	if limiter.Limit("a") != 2 {
		t.Fatalf("success should raise the limit to 2, got %d", limiter.Limit("a"))
	}

	limiter.Observe("a", core.Response{Backend: "a", Outcome: core.Outcome{LatencyMs: 500}}, nil)
	if limiter.Limit("a") != 1 {
		t.Fatalf("slow response should lower the limit to 1, got %d", limiter.Limit("a"))
	}
}

func TestTenantLimiterIsolatesCounters(t *testing.T) {
	limiter := NewTenantLimiter(TenantLimitConfig{
		DefaultTenant: "default",
		Defaults: TenantLimit{
			MaxInFlight: 1,
			MaxCostUSD:  0.10,
		},
		Tenants: map[string]TenantLimit{
			"tenant-b": {
				MaxInFlight: 2,
				MaxCostUSD:  0.20,
			},
		},
	})

	releaseA, ok := limiter.Acquire(core.Request{ID: "a-1", TenantID: "tenant-a"}, "backend")
	if !ok {
		t.Fatal("first tenant-a request should be admitted")
	}
	if _, ok := limiter.Acquire(core.Request{ID: "a-2", TenantID: "tenant-a"}, "backend"); ok {
		t.Fatal("tenant-a should hit its own in-flight limit")
	}
	releaseB1, ok := limiter.Acquire(core.Request{ID: "b-1", TenantID: "tenant-b"}, "backend")
	if !ok {
		t.Fatal("first tenant-b request should be admitted")
	}
	releaseB2, ok := limiter.Acquire(core.Request{ID: "b-2", TenantID: "tenant-b"}, "backend")
	if !ok {
		t.Fatal("tenant-b override should allow a second in-flight request")
	}

	limiter.Observe("backend", core.Response{TenantID: "tenant-a", Outcome: core.Outcome{CostUSD: 0.03}}, nil)
	limiter.Observe("backend", core.Response{TenantID: "tenant-b", Outcome: core.Outcome{CostUSD: 0.04}}, nil)

	if limiter.CostUSD("tenant-a") != 0.03 {
		t.Fatalf("tenant-a cost got %.2f", limiter.CostUSD("tenant-a"))
	}
	if limiter.CostUSD("tenant-b") != 0.04 {
		t.Fatalf("tenant-b cost got %.2f", limiter.CostUSD("tenant-b"))
	}

	releaseA()
	releaseB1()
	releaseB2()
}

func TestTenantLimiterUsesDefaultTenant(t *testing.T) {
	limiter := NewTenantLimiter(TenantLimitConfig{
		DefaultTenant: "default",
		Defaults: TenantLimit{
			MaxInFlight: 1,
		},
	})

	release, ok := limiter.Acquire(core.Request{ID: "req-1"}, "backend")
	if !ok {
		t.Fatal("default tenant request should be admitted")
	}
	if limiter.InFlight("default") != 1 {
		t.Fatalf("default tenant in-flight got %d", limiter.InFlight("default"))
	}
	if _, ok := limiter.Acquire(core.Request{ID: "req-2"}, "backend"); ok {
		t.Fatal("default tenant should hit its own in-flight limit")
	}
	release()
}

func TestGatewayRejectsTenantRequestLimit(t *testing.T) {
	tenantLimiter := NewTenantLimiter(TenantLimitConfig{
		DefaultTenant: "default",
		Defaults: TenantLimit{
			MaxInFlight: 1,
		},
	})
	model := &fakeBackend{id: "a", delay: 50 * time.Millisecond}
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{model},
		Filters:  []Filter{tenantLimiter},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	firstErr := make(chan error, 1)
	go func() {
		_, callErr := gateway.Call(context.Background(), core.Request{ID: "req-1", TenantID: "tenant-a"})
		firstErr <- callErr
	}()
	waitFor(t, func() bool {
		return model.calls.Load() == 1
	})

	_, err = gateway.Call(context.Background(), core.Request{ID: "req-2", TenantID: "tenant-a"})
	if !errors.Is(err, ErrLoadShed) {
		t.Fatalf("tenant request limit error got %v", err)
	}
	if err := <-firstErr; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestGatewayRejectsTenantCostLimit(t *testing.T) {
	tenantLimiter := NewTenantLimiter(TenantLimitConfig{
		DefaultTenant: "default",
		Defaults: TenantLimit{
			MaxCostUSD: 0.01,
		},
	})
	model := &fakeBackend{
		id: "a",
		response: core.Response{
			Outcome: core.Outcome{
				CostUSD: 0.02,
			},
		},
	}
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{model},
		Filters:  []Filter{tenantLimiter},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req-1", TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err = gateway.Call(context.Background(), core.Request{ID: "req-2", TenantID: "tenant-a"})
	if !errors.Is(err, ErrLoadShed) {
		t.Fatalf("tenant cost limit error got %v", err)
	}
}

func TestCircuitBreakerHalfOpenProbe(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	circuit := NewCircuitBreaker([]core.BackendID{"a"}, CircuitConfig{
		FailureThreshold: 2,
		RecoveryAfter:    time.Second,
		HalfOpenMax:      1,
		Clock:            clk,
	})

	failed := core.Response{Backend: "a", Outcome: core.Outcome{Errored: true}}
	circuit.Observe("a", failed, nil)
	if circuit.Mode("a") != "closed" {
		t.Fatalf("one failure should not open the circuit")
	}
	circuit.Observe("a", failed, nil)
	if circuit.Mode("a") != "open" {
		t.Fatalf("two failures should open the circuit")
	}
	if got := circuit.Apply(core.Request{ID: "req"}, []core.BackendID{"a"}); len(got) != 0 {
		t.Fatalf("open circuit should remove candidates, got %v", got)
	}

	clk.Advance(time.Second)
	if got := circuit.Apply(core.Request{ID: "req"}, []core.BackendID{"a"}); len(got) != 1 {
		t.Fatalf("after recovery delay one probe should be eligible, got %v", got)
	}
	release, ok := circuit.Acquire(core.Request{ID: "req-1"}, "a")
	if !ok {
		t.Fatal("half-open probe should be admitted")
	}
	if _, ok := circuit.Acquire(core.Request{ID: "req-2"}, "a"); ok {
		t.Fatal("second half-open probe should be blocked")
	}
	release()

	circuit.Observe("a", core.Response{Backend: "a"}, nil)
	if circuit.Mode("a") != "closed" {
		t.Fatalf("successful probe should close the circuit, got %s", circuit.Mode("a"))
	}
}

func TestGatewayHedgesAndCancelsSlowPrimary(t *testing.T) {
	slow := &fakeBackend{id: "slow", delay: 80 * time.Millisecond}
	fast := &fakeBackend{id: "fast", delay: 5 * time.Millisecond}
	gateway, err := New(Config{
		Router:   router.NewStatic("slow"),
		Backends: []backend.Backend{slow, fast},
		Clock:    clock.NewReal(),
		Hedge: HedgeConfig{
			Enabled: true,
			Delay:   10 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "fast" {
		t.Fatalf("hedge should return the fast backend, got %s", resp.Backend)
	}
	if slow.calls.Load() != 1 || fast.calls.Load() != 1 {
		t.Fatalf("expected one primary and one hedge call, got slow=%d fast=%d", slow.calls.Load(), fast.calls.Load())
	}
	waitFor(t, func() bool {
		return slow.cancels.Load() == 1
	})
}

func TestGatewayDoesNotHedgeWhenDisabled(t *testing.T) {
	slow := &fakeBackend{id: "slow", delay: 20 * time.Millisecond}
	fast := &fakeBackend{id: "fast", delay: time.Millisecond}
	gateway, err := New(Config{
		Router:   router.NewStatic("slow"),
		Backends: []backend.Backend{slow, fast},
		Hedge: HedgeConfig{
			Enabled: false,
			Delay:   time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "slow" {
		t.Fatalf("disabled hedge should keep primary, got %s", resp.Backend)
	}
	if fast.calls.Load() != 0 {
		t.Fatalf("disabled hedge made %d extra calls", fast.calls.Load())
	}
}

func TestGatewayEnforcesHedgeBudget(t *testing.T) {
	budget := 0.0
	slow := &fakeBackend{id: "slow", delay: 20 * time.Millisecond}
	fast := &fakeBackend{id: "fast", delay: time.Millisecond}
	gateway, err := New(Config{
		Router:   router.NewStatic("slow"),
		Backends: []backend.Backend{slow, fast},
		Hedge: HedgeConfig{
			Enabled:        true,
			Delay:          time.Millisecond,
			BudgetFraction: &budget,
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "slow" {
		t.Fatalf("budgeted hedge should keep primary, got %s", resp.Backend)
	}
	if fast.calls.Load() != 0 {
		t.Fatalf("budgeted hedge made %d extra calls", fast.calls.Load())
	}
}

func TestGatewayUsesHedgeTriggerPercentile(t *testing.T) {
	slow := &fakeBackend{id: "slow", delay: 20 * time.Millisecond}
	fast := &fakeBackend{id: "fast", delay: time.Millisecond}
	gateway, err := New(Config{
		Router:   router.NewStatic("slow"),
		Backends: []backend.Backend{slow, fast},
		Hedge: HedgeConfig{
			Enabled:           true,
			Delay:             time.Millisecond,
			TriggerPercentile: 50,
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	gateway.hedgeLatencies.Observe("slow", 80)

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "slow" {
		t.Fatalf("percentile threshold should keep primary, got %s", resp.Backend)
	}
	if fast.calls.Load() != 0 {
		t.Fatalf("percentile threshold made %d extra calls", fast.calls.Load())
	}
}

func TestGatewayHonorsHedgeMaxExtraCalls(t *testing.T) {
	primary := &fakeBackend{id: "primary", delay: 40 * time.Millisecond}
	firstBackup := &fakeBackend{id: "backup-1", delay: 40 * time.Millisecond}
	secondBackup := &fakeBackend{id: "backup-2", delay: time.Millisecond}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, firstBackup, secondBackup},
		Hedge: HedgeConfig{
			Enabled:       true,
			Delay:         time.Millisecond,
			MaxExtraCalls: 1,
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if firstBackup.calls.Load() != 1 {
		t.Fatalf("first backup calls got %d", firstBackup.calls.Load())
	}
	if secondBackup.calls.Load() != 0 {
		t.Fatalf("max extra calls allowed second backup calls: %d", secondBackup.calls.Load())
	}
}

func TestSingleFlightDeduplicatesInFlightRequests(t *testing.T) {
	model := &fakeBackend{id: "a", delay: 50 * time.Millisecond}
	gateway, err := New(Config{
		Router:          router.NewStatic("a"),
		Backends:        []backend.Backend{model},
		SingleFlight:    NewSingleFlight(),
		SingleFlightKey: func(core.Request) string { return "same" },
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	firstErr := make(chan error, 1)
	go func() {
		_, callErr := gateway.Call(context.Background(), core.Request{ID: "first"})
		firstErr <- callErr
	}()

	waitFor(t, func() bool {
		return model.calls.Load() == 1
	})

	_, err = gateway.Call(context.Background(), core.Request{ID: "second"})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if err := <-firstErr; err != nil {
		t.Fatalf("first call: %v", err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("deduped calls should hit backend once, got %d", model.calls.Load())
	}
}

func TestGatewayStreamsRoutedBackend(t *testing.T) {
	model := &fakeStreamBackend{
		fakeBackend: &fakeBackend{id: "a"},
		chunks: []core.StreamChunk{
			{Delta: "hel"},
			{Delta: "lo"},
			{Done: true, Outcome: core.Outcome{LatencyMs: 10, OutputTokens: 2}},
		},
	}
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{model},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	stream, err := gateway.Stream(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("stream: %v", err)
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

	if first.RequestID != "req" || first.Backend != "a" || first.Delta != "hel" {
		t.Fatalf("first chunk got %+v", first)
	}
	if second.Delta != "lo" {
		t.Fatalf("second chunk got %+v", second)
	}
	if !done.Done || done.OutputTokens != 2 {
		t.Fatalf("done chunk got %+v", done)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("stream calls got %d", model.calls.Load())
	}
}

func TestGatewayStreamsFallbackWhenPrimaryFailsBeforeFirstByte(t *testing.T) {
	primary := &fakeStreamBackend{
		fakeBackend: &fakeBackend{id: "primary"},
		streamErr:   fakeStatusError{status: 503},
	}
	backup := &fakeStreamBackend{
		fakeBackend: &fakeBackend{id: "backup"},
		chunks: []core.StreamChunk{
			{Delta: "ok"},
		},
	}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	stream, err := gateway.Stream(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if chunk.Backend != "backup" || chunk.FallbackCount != 1 {
		t.Fatalf("fallback chunk got %+v", chunk)
	}
	if len(chunk.AttemptedBackends) != 2 || primary.calls.Load() != 1 || backup.calls.Load() != 1 {
		t.Fatalf("stream attempts got chunk=%+v primary=%d backup=%d", chunk, primary.calls.Load(), backup.calls.Load())
	}
}

func TestGatewayDoesNotFallbackAfterStreamStarts(t *testing.T) {
	primary := &fakeStreamBackend{
		fakeBackend: &fakeBackend{id: "primary"},
		chunks: []core.StreamChunk{
			{Delta: "partial"},
		},
		recvErr: fakeStatusError{status: 503},
	}
	backup := &fakeStreamBackend{
		fakeBackend: &fakeBackend{id: "backup"},
		chunks: []core.StreamChunk{
			{Delta: "backup"},
		},
	}
	gateway, err := New(Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"primary"},
				Fallbacks:  []core.BackendID{"backup"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	stream, err := gateway.Stream(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("first recv: %v", err)
	}
	if chunk.Delta != "partial" {
		t.Fatalf("first chunk got %+v", chunk)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("stream error should be returned after first byte")
	}
	if backup.calls.Load() != 0 {
		t.Fatalf("backup should not be called after stream starts, got %d", backup.calls.Load())
	}
}

func TestGatewayStreamRejectsUnsupportedBackend(t *testing.T) {
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{instantBackend("a")},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Stream(context.Background(), core.Request{ID: "req"})
	if !errors.Is(err, ErrStreaming) {
		t.Fatalf("stream error got %v", err)
	}
}

func TestGatewayStreamReleasesLimiterAfterDone(t *testing.T) {
	limiter := NewAdaptiveLimiter([]core.BackendID{"a"}, LimitConfig{
		InitialLimit:    1,
		MinLimit:        1,
		MaxLimit:        2,
		TargetLatencyMs: 100,
	})
	model := &fakeStreamBackend{
		fakeBackend: &fakeBackend{id: "a"},
		chunks: []core.StreamChunk{
			{Delta: "hello"},
			{Done: true, Outcome: core.Outcome{LatencyMs: 10, OutputTokens: 1}},
		},
	}
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{model},
		Filters:  []Filter{limiter},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	stream, err := gateway.Stream(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if _, ok := limiter.Acquire(core.Request{ID: "req-2"}, "a"); ok {
		t.Fatal("stream should hold limiter slot")
	}
	stream.Recv()
	stream.Recv()
	if limiter.InFlight("a") != 0 {
		t.Fatalf("stream leaked limiter slot: %d", limiter.InFlight("a"))
	}
}

func TestGatewayConcurrentStressReleasesLimiter(t *testing.T) {
	ids := []core.BackendID{"a", "b", "c"}
	backends := []backend.Backend{
		&fakeBackend{id: "a", delay: time.Millisecond},
		&fakeBackend{id: "b", delay: time.Millisecond},
		&fakeBackend{id: "c", delay: time.Millisecond},
	}
	limiter := NewAdaptiveLimiter(ids, LimitConfig{
		InitialLimit:    100,
		MinLimit:        1,
		MaxLimit:        100,
		TargetLatencyMs: 100,
	})
	gateway, err := New(Config{
		Router:   router.NewP2C(ids, 0.2, 7),
		Backends: backends,
		Filters:  []Filter{limiter},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	var wait sync.WaitGroup
	errs := make(chan error, 120)
	for i := 0; i < 120; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, callErr := gateway.Call(context.Background(), core.Request{ID: "req-" + itoa(index)})
			if callErr != nil {
				errs <- callErr
			}
		}(i)
	}
	wait.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("stress call: %v", err)
	}
	for _, id := range ids {
		if limiter.InFlight(id) != 0 {
			t.Fatalf("backend %s leaked %d in-flight requests", id, limiter.InFlight(id))
		}
	}
}

func TestGatewayEmitsTraceSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{instantBackend("a")},
		Observer: observability.New(observability.Config{
			Name:           "test",
			TracerProvider: traces,
		}),
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	names := endedSpanNames(recorder.Ended())
	if !names["gateway.call"] {
		t.Fatal("gateway call span was not emitted")
	}
	if !names["backend.call"] {
		t.Fatal("backend call span was not emitted")
	}
}

func endedSpanNames(spans []sdktrace.ReadOnlySpan) map[string]bool {
	names := map[string]bool{}
	for _, span := range spans {
		names[span.Name()] = true
	}
	return names
}

func instantBackend(id core.BackendID) backend.Backend {
	return &fakeBackend{id: id}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
