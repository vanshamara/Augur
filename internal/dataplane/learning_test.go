package dataplane

import (
	"context"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

// banditGateway wires a learning router into the data plane so the tests can
// check that learning never escapes the eligible candidate set.
func banditGateway(t *testing.T, config Config) *Gateway {
	t.Helper()
	ids := make([]core.BackendID, 0, len(config.Backends))
	for _, b := range config.Backends {
		ids = append(ids, b.ID())
	}
	config.Router = control.NewBanditRouter(control.BanditConfig{
		Policy:   control.NewPolicy(control.PolicyConfig{}),
		Backends: ids,
		Seed:     7,
	})
	gateway, err := New(config)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	return gateway
}

func TestBanditNeverRoutesToCapabilityFilteredBackend(t *testing.T) {
	chat := instantBackend("chat")
	strong := instantBackend("strong")
	gateway := banditGateway(t, Config{
		Backends: []backend.Backend{chat, strong},
		Capabilities: map[core.BackendID][]core.RequestType{
			"chat":   {core.Chat},
			"strong": {core.Chat, core.Reasoning},
		},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"chat", "strong"},
			},
		},
	})

	for i := 0; i < 200; i++ {
		resp, err := gateway.Call(context.Background(), core.Request{
			ID:       "req-" + itoa(i),
			Features: core.Features{Type: core.Reasoning},
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if resp.Backend != "strong" {
			t.Fatalf("reasoning request routed to %q, which cannot serve reasoning", resp.Backend)
		}
	}
}

func TestBanditNeverRoutesToUnhealthyBackend(t *testing.T) {
	healthy := &fakeBackend{id: "healthy"}
	unhealthy := &fakeBackend{id: "unhealthy"}
	health := NewHealthFilter([]core.BackendID{"healthy", "unhealthy"})
	health.Set("unhealthy", false)
	gateway := banditGateway(t, Config{
		Backends: []backend.Backend{healthy, unhealthy},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"healthy", "unhealthy"},
			},
		},
		Filters: []Filter{health},
	})

	for i := 0; i < 200; i++ {
		resp, err := gateway.Call(context.Background(), core.Request{ID: "req-" + itoa(i)})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if resp.Backend != "healthy" {
			t.Fatalf("request routed to unhealthy backend %q", resp.Backend)
		}
	}
	if unhealthy.calls.Load() != 0 {
		t.Fatalf("unhealthy backend was called %d times", unhealthy.calls.Load())
	}
}

func TestBanditNeverRoutesToOverBudgetBackend(t *testing.T) {
	cheap := &fakeBackend{id: "cheap"}
	expensive := &fakeBackend{id: "expensive"}
	gateway := banditGateway(t, Config{
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

	for i := 0; i < 200; i++ {
		resp, err := gateway.Call(context.Background(), core.Request{
			ID:                  "req-" + itoa(i),
			MaxCompletionTokens: 100,
			Features:            core.Features{PromptTokens: 100, CostBudget: 0.01},
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if resp.Backend != "cheap" {
			t.Fatalf("request routed to over-budget backend %q", resp.Backend)
		}
	}
	if expensive.calls.Load() != 0 {
		t.Fatalf("over-budget backend was called %d times", expensive.calls.Load())
	}
}

func TestBanditHonorsCanaryAssignment(t *testing.T) {
	stable := instantBackend("stable")
	candidate := instantBackend("candidate")
	gateway := banditGateway(t, Config{
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
	})

	resp, err := gateway.Call(context.Background(), core.Request{ID: "req-1"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Backend != "candidate" {
		t.Fatalf("full canary should route to candidate, got %q", resp.Backend)
	}
}
