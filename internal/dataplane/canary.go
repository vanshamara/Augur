package dataplane

import (
	"hash/fnv"
	"sort"
	"strings"
	"sync"

	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

const (
	CanaryModeLive   = "live"
	CanaryModeShadow = "shadow"
)

type CanaryRule struct {
	Backend   core.BackendID
	Percent   float64
	StickyKey string
	Shadow    bool
}

type CanaryConfig struct {
	Rollback control.RollbackConfig
}

type CanaryDecision struct {
	Mode           string
	Backend        core.BackendID
	RollbackReason string
}

type CanaryTable struct {
	mu     sync.Mutex
	guard  *control.RollbackGuard
	routes map[string]*canaryRouteState
}

type canaryRouteState struct {
	baseline canaryWindow
	canary   canaryWindow
	disabled bool
	reason   string
}

type canaryWindow struct {
	samples   int
	errors    int
	latencies []float64
}

func NewCanaryTable(config CanaryConfig) *CanaryTable {
	config.Rollback.MinQuality = 0
	return &CanaryTable{
		guard:  control.NewRollbackGuard(config.Rollback),
		routes: map[string]*canaryRouteState{},
	}
}

func (t *CanaryTable) Disabled(routeName string) (string, bool) {
	if t == nil || routeName == "" {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.route(routeName)
	return state.reason, state.disabled
}

func (t *CanaryTable) Disable(routeName string, reason string) {
	if t == nil || routeName == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.route(routeName)
	state.disabled = true
	state.reason = reason
}

func (t *CanaryTable) Observe(resp core.Response, err error) {
	if t == nil || resp.RouteName == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.route(resp.RouteName)
	if resp.CanaryBackend != "" && resp.Backend == resp.CanaryBackend && (resp.CanaryMode == CanaryModeLive || resp.CanaryMode == CanaryModeShadow) {
		state.canary.add(resp, err)
	} else {
		state.baseline.add(resp, err)
	}

	if state.disabled {
		return
	}
	baseline := state.baseline.snapshot()
	canary := state.canary.snapshot()
	if !t.guard.ShouldRollback(baseline, canary) {
		return
	}
	state.disabled = true
	state.reason = rollbackReason(t.guard.Config(), baseline, canary)
}

func (t *CanaryTable) route(name string) *canaryRouteState {
	state := t.routes[name]
	if state == nil {
		state = &canaryRouteState{}
		t.routes[name] = state
	}
	return state
}

func (w *canaryWindow) add(resp core.Response, err error) {
	w.samples++
	if err != nil || resp.Errored {
		w.errors++
	}
	if resp.LatencyMs > 0 {
		w.latencies = append(w.latencies, resp.LatencyMs)
	}
}

func (w canaryWindow) snapshot() control.SLOSnapshot {
	errorRate := 0.0
	if w.samples > 0 {
		errorRate = float64(w.errors) / float64(w.samples)
	}
	return control.SLOSnapshot{
		Samples:   w.samples,
		P95Ms:     percentile(w.latencies, 95),
		ErrorRate: errorRate,
	}
}

func canaryAssigned(req core.Request, rule CanaryRule) bool {
	if rule.Backend == "" || rule.Percent <= 0 {
		return false
	}
	if rule.Percent >= 100 {
		return true
	}
	return rolloutFraction(stickyValue(req, rule.StickyKey)) < rule.Percent/100
}

func rolloutFraction(value string) float64 {
	hash := fnv.New64a()
	hash.Write([]byte(value))
	return float64(hash.Sum64()%10_000) / 10_000
}

func stickyValue(req core.Request, stickyKey string) string {
	switch strings.TrimSpace(stickyKey) {
	case "tenant_id":
		return fallbackStickyValue(req.TenantID, req.ID)
	case "user_id":
		return fallbackStickyValue(req.UserID, req.ID)
	case "tenant_and_request":
		return fallbackStickyValue(req.TenantID+"\x00"+req.ID, req.ID)
	case "tenant_and_user":
		return fallbackStickyValue(req.TenantID+"\x00"+req.UserID, req.TenantID, req.ID)
	default:
		return fallbackStickyValue(req.ID, req.TenantID, req.UserID)
	}
}

func fallbackStickyValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "\x00" {
			return value
		}
	}
	return "default"
}

func rollbackReason(config control.RollbackConfig, baseline control.SLOSnapshot, canary control.SLOSnapshot) string {
	if baseline.P95Ms > 0 && canary.P95Ms > baseline.P95Ms*(1+config.P95RegressionRatio) {
		return "latency_regression"
	}
	if canary.ErrorRate > config.MaxErrorRate {
		return "error_rate"
	}
	if config.MinQuality > 0 && canary.Quality < config.MinQuality {
		return "quality"
	}
	return "rollback_guard"
}

func annotateCanaryResponse(resp core.Response, decision CanaryDecision) core.Response {
	if decision.Mode != "" {
		resp.CanaryMode = decision.Mode
	}
	if decision.Backend != "" {
		resp.CanaryBackend = decision.Backend
	}
	if decision.RollbackReason != "" {
		resp.CanaryRollback = decision.RollbackReason
	}
	return resp
}

func annotateCanaryChunk(chunk core.StreamChunk, decision CanaryDecision) core.StreamChunk {
	if decision.Mode != "" {
		chunk.CanaryMode = decision.Mode
	}
	if decision.Backend != "" {
		chunk.CanaryBackend = decision.Backend
	}
	if decision.RollbackReason != "" {
		chunk.CanaryRollback = decision.RollbackReason
	}
	return chunk
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int((p / 100) * float64(len(sorted)-1))
	if index < 0 {
		return sorted[0]
	}
	if index >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[index]
}
