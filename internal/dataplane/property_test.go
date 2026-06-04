package dataplane

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

var propertyTaskTypes = []core.RequestType{core.Chat, core.Reasoning, core.Coding}

// TestPropertySelectedBackendSupportsRequestType checks that across many random
// capability layouts the gateway never routes a request to a backend that does
// not support the request type.
func TestPropertySelectedBackendSupportsRequestType(t *testing.T) {
	gen := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 400; i++ {
		ids, backends := propertyBackends(2 + gen.IntN(3))
		capabilities := map[core.BackendID][]core.RequestType{}
		for _, id := range ids {
			capabilities[id] = randomCapabilities(gen)
		}
		gateway, err := New(Config{
			Router:       router.NewRoundRobin(),
			Backends:     backends,
			Capabilities: capabilities,
			Routes:       []RouteRule{{Name: "default", Candidates: ids}},
		})
		if err != nil {
			t.Fatalf("new gateway: %v", err)
		}

		requestType := propertyTaskTypes[gen.IntN(len(propertyTaskTypes))]
		resp, callErr := gateway.Call(context.Background(), core.Request{
			ID:       "cap-" + itoa(i),
			Features: core.Features{Type: requestType},
		})

		if !anyBackendSupportsType(capabilities, ids, requestType) {
			if !errors.Is(callErr, ErrNoCompatibleCandidates) {
				t.Fatalf("no backend supports %q but error was %v", requestType, callErr)
			}
			continue
		}
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if !backendSupportsType(capabilities[resp.Backend], requestType) {
			t.Fatalf("routed %q to %q with capabilities %v", requestType, resp.Backend, capabilities[resp.Backend])
		}
	}
}

// TestPropertySelectedBackendIsHealthy checks that the gateway never routes to a
// backend the health filter has marked unhealthy.
func TestPropertySelectedBackendIsHealthy(t *testing.T) {
	gen := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 400; i++ {
		ids, backends := propertyBackends(2 + gen.IntN(3))
		health := NewHealthFilter(ids)
		healthy := map[core.BackendID]bool{}
		anyHealthy := false
		for _, id := range ids {
			ok := gen.IntN(2) == 0
			health.Set(id, ok)
			healthy[id] = ok
			anyHealthy = anyHealthy || ok
		}
		gateway, err := New(Config{
			Router:   router.NewRoundRobin(),
			Backends: backends,
			Routes:   []RouteRule{{Name: "default", Candidates: ids}},
			Filters:  []Filter{health},
		})
		if err != nil {
			t.Fatalf("new gateway: %v", err)
		}

		resp, callErr := gateway.Call(context.Background(), core.Request{ID: "health-" + itoa(i)})
		if !anyHealthy {
			if !errors.Is(callErr, ErrNoCandidates) {
				t.Fatalf("all backends unhealthy but error was %v", callErr)
			}
			continue
		}
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if !healthy[resp.Backend] {
			t.Fatalf("routed to unhealthy backend %q", resp.Backend)
		}
	}
}

// TestPropertySelectedBackendWithinBudget checks that the selected backend's
// estimated max cost never exceeds the request cost budget.
func TestPropertySelectedBackendWithinBudget(t *testing.T) {
	gen := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 400; i++ {
		ids, backends := propertyBackends(2 + gen.IntN(3))
		pricing := map[core.BackendID]BackendPrice{}
		for _, id := range ids {
			pricing[id] = BackendPrice{
				InputPerToken:  gen.Float64() * 0.0002,
				OutputPerToken: gen.Float64() * 0.0002,
			}
		}
		gateway, err := New(Config{
			Router:   router.NewRoundRobin(),
			Backends: backends,
			Routes:   []RouteRule{{Name: "default", Candidates: ids}},
			Pricing:  pricing,
		})
		if err != nil {
			t.Fatalf("new gateway: %v", err)
		}

		promptTokens := 50 + gen.IntN(200)
		maxTokens := 50 + gen.IntN(200)
		budget := 0.0001 + gen.Float64()*0.05
		req := core.Request{
			ID:                  "budget-" + itoa(i),
			MaxCompletionTokens: maxTokens,
			Features:            core.Features{PromptTokens: promptTokens, CostBudget: budget},
		}
		resp, callErr := gateway.Call(context.Background(), req)

		affordable := false
		for _, id := range ids {
			if estimateForTest(pricing[id], promptTokens, maxTokens) <= budget {
				affordable = true
			}
		}
		if !affordable {
			if !errors.Is(callErr, ErrOverBudget) {
				t.Fatalf("no backend fits budget %.6f but error was %v", budget, callErr)
			}
			continue
		}
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if estimate := estimateForTest(pricing[resp.Backend], promptTokens, maxTokens); estimate > budget {
			t.Fatalf("selected %q with estimate %.6f over budget %.6f", resp.Backend, estimate, budget)
		}
	}
}

// TestPropertyCanaryShareMatchesPercent checks that a canary sends about the
// configured percent of traffic to the candidate over a large deterministic run.
func TestPropertyCanaryShareMatchesPercent(t *testing.T) {
	for _, percent := range []float64{5, 25, 50} {
		gateway, err := New(Config{
			Router:   router.NewStatic("stable"),
			Backends: []backend.Backend{instantBackend("stable"), instantBackend("candidate")},
			Routes: []RouteRule{{
				Name:       "default",
				Candidates: []core.BackendID{"stable"},
				Canary:     CanaryRule{Backend: "candidate", Percent: percent},
			}},
		})
		if err != nil {
			t.Fatalf("new gateway: %v", err)
		}

		const total = 4000
		hits := 0
		for i := 0; i < total; i++ {
			resp, callErr := gateway.Call(context.Background(), core.Request{ID: "canary-" + itoa(int(percent)) + "-" + itoa(i)})
			if callErr != nil {
				t.Fatalf("call: %v", callErr)
			}
			if resp.Backend == "candidate" {
				hits++
			}
		}
		share := float64(hits) / total * 100
		if math.Abs(share-percent) > 2 {
			t.Fatalf("percent %.0f got %.1f%% over %d requests", percent, share, total)
		}
	}
}

func propertyBackends(count int) ([]core.BackendID, []backend.Backend) {
	ids := make([]core.BackendID, count)
	backends := make([]backend.Backend, count)
	for i := 0; i < count; i++ {
		id := core.BackendID("b" + itoa(i))
		ids[i] = id
		backends[i] = &fakeBackend{id: id}
	}
	return ids, backends
}

func randomCapabilities(gen *rand.Rand) []core.RequestType {
	if gen.IntN(4) == 0 {
		return nil
	}
	out := []core.RequestType{}
	for _, requestType := range propertyTaskTypes {
		if gen.IntN(2) == 0 {
			out = append(out, requestType)
		}
	}
	if len(out) == 0 {
		out = append(out, propertyTaskTypes[gen.IntN(len(propertyTaskTypes))])
	}
	return out
}

func backendSupportsType(capabilities []core.RequestType, requestType core.RequestType) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, capability := range capabilities {
		if capability == requestType {
			return true
		}
	}
	return false
}

func anyBackendSupportsType(capabilities map[core.BackendID][]core.RequestType, ids []core.BackendID, requestType core.RequestType) bool {
	for _, id := range ids {
		if backendSupportsType(capabilities[id], requestType) {
			return true
		}
	}
	return false
}

func estimateForTest(price BackendPrice, promptTokens int, maxTokens int) float64 {
	return float64(promptTokens)*price.InputPerToken + float64(maxTokens)*price.OutputPerToken
}
