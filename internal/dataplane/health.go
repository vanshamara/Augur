package dataplane

import (
	"sync"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

type HealthFilter struct {
	filterBase
	mu     sync.RWMutex
	states map[core.BackendID]*HealthState
}

type HealthState struct {
	Healthy              bool
	LastChecked          time.Time
	LastError            string
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
}

func NewHealthFilter(ids []core.BackendID) *HealthFilter {
	states := make(map[core.BackendID]*HealthState, len(ids))
	for _, id := range ids {
		states[id] = &HealthState{Healthy: true}
	}
	return &HealthFilter{states: states}
}

func (h *HealthFilter) Name() string {
	return "health"
}

func (h *HealthFilter) Apply(req core.Request, candidates []core.BackendID) []core.BackendID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]core.BackendID, 0, len(candidates))
	for _, id := range candidates {
		state := h.states[id]
		if state != nil && state.Healthy {
			out = append(out, id)
		}
	}
	return out
}

func (h *HealthFilter) Set(id core.BackendID, healthy bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.state(id)
	state.Healthy = healthy
	if healthy {
		state.LastError = ""
	}
}

func (h *HealthFilter) Healthy(id core.BackendID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.states[id]
	return state != nil && state.Healthy
}

func (h *HealthFilter) RecordCheck(id core.BackendID, healthy bool, checkedAt time.Time, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state := h.state(id)
	state.Healthy = healthy
	state.LastChecked = checkedAt
	if err == nil {
		state.LastError = ""
		return
	}
	state.LastError = err.Error()
}

func (h *HealthFilter) SetCounters(id core.BackendID, failures int, successes int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state := h.state(id)
	state.ConsecutiveFailures = failures
	state.ConsecutiveSuccesses = successes
}

func (h *HealthFilter) Status(id core.BackendID) HealthState {
	h.mu.RLock()
	defer h.mu.RUnlock()

	state := h.states[id]
	if state == nil {
		return HealthState{}
	}
	return *state
}

func (h *HealthFilter) state(id core.BackendID) *HealthState {
	state := h.states[id]
	if state == nil {
		state = &HealthState{}
		h.states[id] = state
	}
	return state
}
