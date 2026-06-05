package dataplane

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

// TestCircuitBreakerErrorRateAblation drives round-robin traffic through a
// two-backend fleet where one backend always errors, with and without the
// circuit breaker, and reports the error-rate delta. It is a controlled ablation
// that isolates the breaker's effect, not a production measurement.
func TestCircuitBreakerErrorRateAblation(t *testing.T) {
	const requests = 2000

	baseline := ablationErrorRate(t, false, requests)
	withBreaker := ablationErrorRate(t, true, requests)

	t.Logf("error rate without breaker: %.1f%%", baseline*100)
	t.Logf("error rate with breaker:    %.2f%%", withBreaker*100)
	if withBreaker > 0 {
		t.Logf("reduction: %.0fx", baseline/withBreaker)
	}

	if withBreaker >= baseline {
		t.Fatalf("breaker did not reduce error rate: with=%.3f baseline=%.3f", withBreaker, baseline)
	}
}

func ablationErrorRate(t *testing.T, breaker bool, requests int) float64 {
	t.Helper()
	good := &fakeBackend{id: "good"}
	bad := &fakeBackend{id: "bad", err: errors.New("backend down")}

	var filters []Filter
	if breaker {
		filters = append(filters, NewCircuitBreaker([]core.BackendID{"good", "bad"}, CircuitConfig{FailureThreshold: 5}))
	}

	gateway, err := New(Config{
		Router:   router.NewRoundRobin(),
		Backends: []backend.Backend{good, bad},
		Filters:  filters,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	failed := 0
	for i := 0; i < requests; i++ {
		req := core.Request{ID: fmt.Sprintf("req-%d", i)}
		resp, callErr := gateway.Call(context.Background(), req)
		if callErr != nil || resp.Errored {
			failed++
		}
	}
	return float64(failed) / float64(requests)
}
