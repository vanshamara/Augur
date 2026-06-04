package dataplane

import (
	"sort"
	"sync"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

const backendStatusWindow = 128

type BackendStatus struct {
	ID                     core.BackendID `json:"id"`
	Healthy                bool           `json:"healthy"`
	LastHealthCheck        time.Time      `json:"last_health_check,omitempty"`
	HealthError            string         `json:"health_error,omitempty"`
	ConsecutiveFailures    int            `json:"consecutive_failures"`
	ConsecutiveSuccesses   int            `json:"consecutive_successes"`
	CircuitMode            string         `json:"circuit_mode,omitempty"`
	ConcurrencyInFlight    int64          `json:"concurrency_in_flight,omitempty"`
	ConcurrencyLimit       int64          `json:"concurrency_limit,omitempty"`
	Samples                int            `json:"samples"`
	P95LatencyMs           float64        `json:"p95_latency_ms,omitempty"`
	ErrorRate              float64        `json:"error_rate"`
	LastError              string         `json:"last_error,omitempty"`
	BackendTimeoutMs       int64          `json:"backend_timeout_ms,omitempty"`
	ActiveHealthConfigured bool           `json:"active_health_configured"`
}

type backendStatusTable struct {
	mu      sync.Mutex
	states  map[core.BackendID]*backendWindow
	window  int
	timeOut map[core.BackendID]time.Duration
}

type backendWindow struct {
	samples   []backendSample
	lastError string
}

type backendSample struct {
	latencyMs float64
	errored   bool
}

func newBackendStatusTable(ids []core.BackendID, timeouts map[core.BackendID]time.Duration) *backendStatusTable {
	states := make(map[core.BackendID]*backendWindow, len(ids))
	for _, id := range ids {
		states[id] = &backendWindow{}
	}
	return &backendStatusTable{
		states:  states,
		window:  backendStatusWindow,
		timeOut: copyTimeouts(timeouts),
	}
}

func (t *backendStatusTable) Observe(id core.BackendID, resp core.Response, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.state(id)
	state.samples = append(state.samples, backendSample{
		latencyMs: resp.LatencyMs,
		errored:   err != nil || resp.Errored,
	})
	if len(state.samples) > t.window {
		copy(state.samples, state.samples[len(state.samples)-t.window:])
		state.samples = state.samples[:t.window]
	}
	if err != nil {
		state.lastError = err.Error()
	}
	if err == nil && !resp.Errored {
		state.lastError = ""
	}
}

func (t *backendStatusTable) Snapshot(id core.BackendID) BackendStatus {
	if t == nil {
		return BackendStatus{ID: id}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.state(id)
	status := BackendStatus{
		ID:               id,
		Samples:          len(state.samples),
		ErrorRate:        errorRate(state.samples),
		LastError:        state.lastError,
		BackendTimeoutMs: durationMillis(t.timeOut[id]),
	}
	status.P95LatencyMs = sampleP95(state.samples)
	return status
}

func (t *backendStatusTable) state(id core.BackendID) *backendWindow {
	state := t.states[id]
	if state == nil {
		state = &backendWindow{}
		t.states[id] = state
	}
	return state
}

func sampleP95(samples []backendSample) float64 {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.latencyMs > 0 {
			values = append(values, sample.latencyMs)
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	index := (95*len(values)+99)/100 - 1
	return values[index]
}

func errorRate(samples []backendSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	errors := 0
	for _, sample := range samples {
		if sample.errored {
			errors++
		}
	}
	return float64(errors) / float64(len(samples))
}

func copyTimeouts(values map[core.BackendID]time.Duration) map[core.BackendID]time.Duration {
	out := make(map[core.BackendID]time.Duration, len(values))
	for id, value := range values {
		out[id] = value
	}
	return out
}

func durationMillis(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value / time.Millisecond)
}
