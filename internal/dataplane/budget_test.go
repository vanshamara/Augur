package dataplane

import (
	"context"
	"errors"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

func budgetRequest(costBudget float64) core.Request {
	return core.Request{
		ID:                  "req",
		MaxCompletionTokens: 100,
		Features: core.Features{
			Type:         core.Chat,
			PromptTokens: 100,
			CostBudget:   costBudget,
		},
	}
}

func TestGatewayExcludesOverBudgetBackend(t *testing.T) {
	cheap := &fakeBackend{id: "cheap"}
	expensive := &fakeBackend{id: "expensive"}
	gateway, err := New(Config{
		Router:   router.NewStatic("expensive"),
		Backends: []backend.Backend{cheap, expensive},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"cheap", "expensive"},
			},
		},
		Pricing: map[core.BackendID]BackendPrice{
			"cheap":     {InputPerToken: 0.00001, OutputPerToken: 0.00001},
			"expensive": {InputPerToken: 0.001, OutputPerToken: 0.001},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), budgetRequest(0.01))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "cheap" {
		t.Fatalf("expected cheap backend, got %q", resp.Backend)
	}
	if expensive.calls.Load() != 0 {
		t.Fatalf("over-budget backend was called %d times", expensive.calls.Load())
	}
}

func TestGatewayReturnsOverBudgetWhenAllExcluded(t *testing.T) {
	expensive := &fakeBackend{id: "expensive"}
	gateway, err := New(Config{
		Router:   router.NewStatic("expensive"),
		Backends: []backend.Backend{expensive},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"expensive"},
			},
		},
		Pricing: map[core.BackendID]BackendPrice{
			"expensive": {InputPerToken: 0.001, OutputPerToken: 0.001},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), budgetRequest(0.0001))
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("expected over-budget error, got %v", err)
	}
	if expensive.calls.Load() != 0 {
		t.Fatalf("over-budget backend was called %d times", expensive.calls.Load())
	}
}

func TestGatewayKeepsBackendWithUnknownPrice(t *testing.T) {
	unpriced := &fakeBackend{id: "unpriced"}
	gateway, err := New(Config{
		Router:   router.NewStatic("unpriced"),
		Backends: []backend.Backend{unpriced},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"unpriced"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), budgetRequest(0.0001))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "unpriced" {
		t.Fatalf("expected unpriced backend to stay eligible, got %q", resp.Backend)
	}
}

func TestGatewayRequiresPricingForBudgetedRequest(t *testing.T) {
	unpriced := &fakeBackend{id: "unpriced"}
	gateway, err := New(Config{
		Router:         router.NewStatic("unpriced"),
		Backends:       []backend.Backend{unpriced},
		RequirePricing: true,
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"unpriced"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), budgetRequest(0.01))
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("expected over-budget error for unpriced backend, got %v", err)
	}
	if unpriced.calls.Load() != 0 {
		t.Fatalf("unpriced backend was called %d times", unpriced.calls.Load())
	}
}

func TestGatewayAllowsUnpricedBackendWithoutBudgetWhenPricingRequired(t *testing.T) {
	unpriced := &fakeBackend{id: "unpriced"}
	gateway, err := New(Config{
		Router:         router.NewStatic("unpriced"),
		Backends:       []backend.Backend{unpriced},
		RequirePricing: true,
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"unpriced"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), budgetRequest(0))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "unpriced" {
		t.Fatalf("backend got %q", resp.Backend)
	}
}

func TestGatewaySkipsOverBudgetCanaryForOneRequest(t *testing.T) {
	stable := &fakeBackend{id: "stable"}
	candidate := &fakeBackend{id: "candidate"}
	gateway, err := New(Config{
		Router:   router.NewStatic("stable"),
		Backends: []backend.Backend{stable, candidate},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"stable"},
				Canary: CanaryRule{
					Backend: "candidate",
					Percent: 100,
				},
			},
		},
		Pricing: map[core.BackendID]BackendPrice{
			"stable":    {InputPerToken: 0.00001, OutputPerToken: 0.00001},
			"candidate": {InputPerToken: 0.001, OutputPerToken: 0.001},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), budgetRequest(0.01))
	if err != nil {
		t.Fatalf("low-budget call: %v", err)
	}
	if resp.Backend != "stable" || resp.CanaryRollback != canaryRollbackOverBudget {
		t.Fatalf("low-budget response got %+v", resp)
	}
	if candidate.calls.Load() != 0 {
		t.Fatalf("over-budget canary was called %d times", candidate.calls.Load())
	}

	req := budgetRequest(1)
	req.ID = "high-budget"
	resp, err = gateway.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("high-budget call: %v", err)
	}
	if resp.Backend != "candidate" || resp.CanaryMode != CanaryModeLive {
		t.Fatalf("high-budget response got %+v", resp)
	}
}

func TestGatewayShedsTenantOverCostLimit(t *testing.T) {
	model := &fakeBackend{
		id:       "a",
		response: core.Response{Backend: "a", Outcome: core.Outcome{CostUSD: 0.02}},
	}
	tenantLimiter := NewTenantLimiter(TenantLimitConfig{
		DefaultTenant: "default",
		Tenants: map[string]TenantLimit{
			"tenant-a": {MaxCostUSD: 0.01},
		},
	})
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{model},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"a"},
			},
		},
		Filters: []Filter{tenantLimiter},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	first, err := gateway.Call(context.Background(), core.Request{ID: "req-1", TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first.Backend != "a" {
		t.Fatalf("first call backend got %q", first.Backend)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req-2", TenantID: "tenant-a"})
	if !errors.Is(err, ErrLoadShed) {
		t.Fatalf("expected load shed once tenant cost limit is spent, got %v", err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("backend was called %d times after the budget was spent", model.calls.Load())
	}
}

func TestGatewayStampsEstimatedCost(t *testing.T) {
	priced := &fakeBackend{
		id:       "priced",
		response: core.Response{Backend: "priced", Outcome: core.Outcome{CostUSD: 0.0015}},
	}
	gateway, err := New(Config{
		Router:   router.NewStatic("priced"),
		Backends: []backend.Backend{priced},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"priced"},
			},
		},
		Pricing: map[core.BackendID]BackendPrice{
			"priced": {InputPerToken: 0.00001, OutputPerToken: 0.00002},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	resp, err := gateway.Call(context.Background(), budgetRequest(0))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	wantEstimate := 100*0.00001 + 100*0.00002
	if resp.EstimatedCostUSD != wantEstimate {
		t.Fatalf("estimated cost got %v, want %v", resp.EstimatedCostUSD, wantEstimate)
	}
	if resp.CostUSD != 0.0015 {
		t.Fatalf("realized cost got %v, want 0.0015", resp.CostUSD)
	}
}
