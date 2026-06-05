package dataplane

import (
	"context"
	"fmt"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

// BenchmarkGatewayDecision measures the full routing decision a request runs
// before any provider call: route selection, capability match, the
// health/circuit/concurrency/tenant filter chain, cost-budget filtering, canary,
// and the router pick. It excludes the backend call itself, so the result is the
// gateway's end-to-end routing overhead. Parametrized by backend count because
// the filter chain and pick are linear in the number of candidates.
func BenchmarkGatewayDecision(b *testing.B) {
	for _, n := range []int{3, 8, 16} {
		b.Run(fmt.Sprintf("backends=%d", n), func(b *testing.B) {
			ids := make([]core.BackendID, n)
			backends := make([]backend.Backend, n)
			pricing := make(map[core.BackendID]BackendPrice, n)
			for i := range ids {
				id := core.BackendID(fmt.Sprintf("backend-%d", i))
				ids[i] = id
				backends[i] = instantBackend(id)
				pricing[id] = BackendPrice{InputPerToken: 1e-6, OutputPerToken: 2e-6, MaxOutputTokens: 500}
			}

			gateway, err := New(Config{
				Router:   router.NewRoundRobin(),
				Backends: backends,
				Pricing:  pricing,
				Filters: []Filter{
					NewHealthFilter(ids),
					NewCircuitBreaker(ids, CircuitConfig{}),
					NewAdaptiveLimiter(ids, LimitConfig{InitialLimit: 100, MinLimit: 1, MaxLimit: 1000}),
					NewTenantLimiter(TenantLimitConfig{}),
				},
			})
			if err != nil {
				b.Fatal(err)
			}

			ctx := context.Background()
			req := core.Request{
				ID:       "bench",
				Features: core.Features{Type: core.Chat, PromptTokens: 100, LatencyBudgetMs: 1000, CostBudget: 0.05},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				set := gateway.candidates(req)
				if set.Err != nil {
					b.Fatal(set.Err)
				}
				if gateway.router.Pick(ctx, req, set.IDs) == "" {
					b.Fatal("expected a backend")
				}
			}
		})
	}
}
