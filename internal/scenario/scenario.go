package scenario

import (
	"context"
	"fmt"
	"sort"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/router"
)

// ScopeNote tells the reader these scenarios use scripted in-memory backends so
// no real provider is called and the results are reproducible.
const ScopeNote = "scope: scenarios use scripted in-memory backends. No real provider APIs are called, so results are deterministic and free."

// Result is the outcome of one product-promise scenario.
type Result struct {
	Name    string
	Promise string
	OK      bool
	Summary string
	Detail  []string
}

// Run executes every product-promise scenario and returns one result each.
func Run() []Result {
	return []Result{
		healthScenario(),
		latencyScenario(),
		costScenario(),
		taskScenario(),
		canaryScenario(),
		fallbackScenario(),
	}
}

func healthScenario() Result {
	result := Result{Name: "health", Promise: "A dead backend is avoided before user traffic hits it."}
	primary := &scriptBackend{id: "primary", checkErr: fmt.Errorf("connection refused")}
	backup := &scriptBackend{id: "backup", latencyMs: 120, costUSD: 0.0002}
	health := dataplane.NewHealthFilter([]core.BackendID{"primary", "backup"})

	active := dataplane.NewActiveHealth(dataplane.ActiveHealthConfig{FailureThreshold: 1}, health, []backend.Backend{primary, backup})
	active.CheckOnce(context.Background())

	gateway, err := dataplane.New(dataplane.Config{
		Router:   router.NewRoundRobin(),
		Backends: []backend.Backend{primary, backup},
		Routes:   []dataplane.RouteRule{{Name: "default", Candidates: []core.BackendID{"primary", "backup"}}},
		Filters:  []dataplane.Filter{health},
	})
	if err != nil {
		return failed(result, err)
	}

	counts := callMany(gateway, 20, func(i int) core.Request {
		return core.Request{ID: fmt.Sprintf("health-%d", i)}
	})
	result.OK = counts["primary"] == 0 && counts["backup"] == 20
	result.Summary = fmt.Sprintf("active health check marked %q unhealthy, so all 20 requests went to %q", "primary", "backup")
	result.Detail = []string{
		"active health check failure on primary: connection refused",
		distribution(counts),
	}
	return result
}

func latencyScenario() Result {
	result := Result{Name: "latency", Promise: "A slow backend receives less traffic over time."}
	fast := &scriptBackend{id: "fast", latencyMs: 80, costUSD: 0.0003}
	slow := &scriptBackend{id: "slow", latencyMs: 900, costUSD: 0.0003}
	gateway, err := dataplane.New(dataplane.Config{
		Router:   router.NewEWMA([]core.BackendID{"fast", "slow"}, 0.3),
		Backends: []backend.Backend{fast, slow},
		Routes:   []dataplane.RouteRule{{Name: "default", Candidates: []core.BackendID{"fast", "slow"}}},
	})
	if err != nil {
		return failed(result, err)
	}

	const total = 100
	counts := callMany(gateway, total, func(i int) core.Request {
		return core.Request{ID: fmt.Sprintf("latency-%d", i)}
	})
	result.OK = counts["fast"] > counts["slow"]*3
	result.Summary = fmt.Sprintf("EWMA routing sent %d%% of %d requests to the fast backend", counts["fast"]*100/total, total)
	result.Detail = []string{
		"fast backend latency 80 ms, slow backend latency 900 ms",
		distribution(counts),
	}
	return result
}

func costScenario() Result {
	result := Result{Name: "cost", Promise: "Over-budget backends are excluded before the call."}
	cheap := &scriptBackend{id: "cheap", latencyMs: 200, costUSD: 0.0008}
	expensive := &scriptBackend{id: "expensive", latencyMs: 150, costUSD: 0.02}
	gateway, err := dataplane.New(dataplane.Config{
		Router:   router.NewRoundRobin(),
		Backends: []backend.Backend{cheap, expensive},
		Routes:   []dataplane.RouteRule{{Name: "default", Candidates: []core.BackendID{"cheap", "expensive"}}},
		Pricing: map[core.BackendID]dataplane.BackendPrice{
			"cheap":     {InputPerToken: 0.000002, OutputPerToken: 0.000008},
			"expensive": {InputPerToken: 0.00006, OutputPerToken: 0.00012},
		},
	})
	if err != nil {
		return failed(result, err)
	}

	counts := callMany(gateway, 20, func(i int) core.Request {
		return core.Request{
			ID:                  fmt.Sprintf("cost-%d", i),
			MaxCompletionTokens: 256,
			Features:            core.Features{PromptTokens: 256, CostBudget: 0.01},
		}
	})
	result.OK = counts["expensive"] == 0 && counts["cheap"] == 20
	result.Summary = fmt.Sprintf("with a $0.01 budget, all 20 requests went to %q and the expensive backend was excluded", "cheap")
	result.Detail = []string{
		"cheap estimated max cost about $0.0026, expensive about $0.046",
		distribution(counts),
	}
	return result
}

func taskScenario() Result {
	result := Result{Name: "task-type", Promise: "A reasoning request never routes to a chat-only backend."}
	chatOnly := &scriptBackend{id: "chat-only", latencyMs: 120, costUSD: 0.0002}
	strong := &scriptBackend{id: "strong", latencyMs: 300, costUSD: 0.002}
	gateway, err := dataplane.New(dataplane.Config{
		Router:   router.NewRoundRobin(),
		Backends: []backend.Backend{chatOnly, strong},
		Capabilities: map[core.BackendID][]core.RequestType{
			"chat-only": {core.Chat},
			"strong":    {core.Chat, core.Reasoning},
		},
		Routes: []dataplane.RouteRule{{Name: "default", Candidates: []core.BackendID{"chat-only", "strong"}}},
	})
	if err != nil {
		return failed(result, err)
	}

	counts := callMany(gateway, 20, func(i int) core.Request {
		return core.Request{ID: fmt.Sprintf("task-%d", i), Features: core.Features{Type: core.Reasoning}}
	})
	result.OK = counts["chat-only"] == 0 && counts["strong"] == 20
	result.Summary = fmt.Sprintf("all 20 reasoning requests went to %q because %q only supports chat", "strong", "chat-only")
	result.Detail = []string{
		"capabilities: chat-only=[chat], strong=[chat, reasoning]",
		distribution(counts),
	}
	return result
}

func canaryScenario() Result {
	result := Result{Name: "canary", Promise: "A 5 percent canary is stable and reproducible."}
	build := func() (*dataplane.Gateway, error) {
		stable := &scriptBackend{id: "stable", latencyMs: 200, costUSD: 0.0005}
		candidate := &scriptBackend{id: "candidate", latencyMs: 210, costUSD: 0.0005}
		return dataplane.New(dataplane.Config{
			Router:   router.NewStatic("stable"),
			Backends: []backend.Backend{stable, candidate},
			Routes: []dataplane.RouteRule{{
				Name:       "default",
				Candidates: []core.BackendID{"stable"},
				Canary:     dataplane.CanaryRule{Backend: "candidate", Percent: 5},
			}},
		})
	}

	const total = 2000
	first, err := build()
	if err != nil {
		return failed(result, err)
	}
	second, err := build()
	if err != nil {
		return failed(result, err)
	}
	firstCounts := callMany(first, total, canaryRequest)
	secondCounts := callMany(second, total, canaryRequest)

	share := float64(firstCounts["candidate"]) / float64(total) * 100
	result.OK = firstCounts["candidate"] == secondCounts["candidate"] && share >= 3 && share <= 7
	result.Summary = fmt.Sprintf("a 5%% canary sent %.1f%% of %d requests to the candidate, identical across two runs", share, total)
	result.Detail = []string{
		fmt.Sprintf("run one: %d to candidate, run two: %d to candidate", firstCounts["candidate"], secondCounts["candidate"]),
		distribution(firstCounts),
	}
	return result
}

func fallbackScenario() Result {
	result := Result{Name: "fallback", Promise: "A failed primary falls back to a configured backup."}
	primary := &scriptBackend{id: "primary", callErr: statusError{code: 503}, errored: true}
	backup := &scriptBackend{id: "backup", latencyMs: 150, costUSD: 0.0004}
	gateway, err := dataplane.New(dataplane.Config{
		Router:   router.NewStatic("primary"),
		Backends: []backend.Backend{primary, backup},
		Routes: []dataplane.RouteRule{{
			Name:       "default",
			Candidates: []core.BackendID{"primary"},
			Fallbacks:  []core.BackendID{"backup"},
		}},
	})
	if err != nil {
		return failed(result, err)
	}

	resp, callErr := gateway.Call(context.Background(), core.Request{ID: "fallback-1"})
	if callErr != nil {
		return failed(result, callErr)
	}
	result.OK = resp.Backend == "backup" && resp.FallbackCount == 1 && len(resp.AttemptedBackends) == 2
	result.Summary = fmt.Sprintf("primary returned 503, so Augur fell back to %q after %d retry", "backup", resp.FallbackCount)
	result.Detail = []string{
		fmt.Sprintf("attempted backends: %s", joinIDs(resp.AttemptedBackends)),
	}
	return result
}

func canaryRequest(i int) core.Request {
	return core.Request{ID: fmt.Sprintf("canary-%d", i)}
}

func callMany(gateway *dataplane.Gateway, count int, build func(i int) core.Request) map[core.BackendID]int {
	counts := map[core.BackendID]int{}
	for i := 0; i < count; i++ {
		resp, err := gateway.Call(context.Background(), build(i))
		if err != nil {
			counts["error"]++
			continue
		}
		counts[resp.Backend]++
	}
	return counts
}

func distribution(counts map[core.BackendID]int) string {
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s=%d", id, counts[core.BackendID(id)]))
	}
	return "routed: " + joinStrings(parts, ", ")
}

func failed(result Result, err error) Result {
	result.OK = false
	result.Summary = "scenario setup failed: " + err.Error()
	return result
}

func joinIDs(ids []core.BackendID) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, string(id))
	}
	return joinStrings(parts, ", ")
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += sep
		}
		out += part
	}
	return out
}
