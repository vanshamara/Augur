package dataplane

import (
	"context"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

// BenchmarkGatewayCall measures the full request path against a zero-latency
// backend: route, capability, the filter chain, acquire/release, the backend
// call, router observation, and status update. With an instant backend, ns/op is
// the latency the gateway adds over a direct provider call, and 1e9/ns is the
// sustained single-goroutine request rate.
func BenchmarkGatewayCall(b *testing.B) {
	gateway := benchGateway(b)
	ctx := context.Background()
	req := core.Request{ID: "bench", Features: core.Features{Type: core.Chat, PromptTokens: 100}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := gateway.Call(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGatewayCallParallel reports aggregate throughput under concurrency.
// 1e9/ns is the sustained request rate across all goroutines.
func BenchmarkGatewayCallParallel(b *testing.B) {
	gateway := benchGateway(b)
	req := core.Request{ID: "bench", Features: core.Features{Type: core.Chat, PromptTokens: 100}}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			if _, err := gateway.Call(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchGateway(b *testing.B) *Gateway {
	b.Helper()
	ids := []core.BackendID{"a", "b", "c"}
	backends := []backend.Backend{instantBackend("a"), instantBackend("b"), instantBackend("c")}
	gateway, err := New(Config{
		Router:   router.NewRoundRobin(),
		Backends: backends,
		Filters: []Filter{
			NewHealthFilter(ids),
			NewCircuitBreaker(ids, CircuitConfig{}),
			NewAdaptiveLimiter(ids, LimitConfig{InitialLimit: 1000, MinLimit: 1, MaxLimit: 10000}),
			NewTenantLimiter(TenantLimitConfig{}),
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	return gateway
}
