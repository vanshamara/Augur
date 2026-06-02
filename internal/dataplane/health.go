package dataplane

import (
	"sync"

	"github.com/vanshamara/Augur/internal/core"
)

type HealthFilter struct {
	filterBase
	mu      sync.RWMutex
	healthy map[core.BackendID]bool
}

func NewHealthFilter(ids []core.BackendID) *HealthFilter {
	healthy := make(map[core.BackendID]bool, len(ids))
	for _, id := range ids {
		healthy[id] = true
	}
	return &HealthFilter{healthy: healthy}
}

func (h *HealthFilter) Name() string {
	return "health"
}

func (h *HealthFilter) Apply(req core.Request, candidates []core.BackendID) []core.BackendID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]core.BackendID, 0, len(candidates))
	for _, id := range candidates {
		if h.healthy[id] {
			out = append(out, id)
		}
	}
	return out
}

func (h *HealthFilter) Set(id core.BackendID, healthy bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthy[id] = healthy
}

func (h *HealthFilter) Healthy(id core.BackendID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.healthy[id]
}
