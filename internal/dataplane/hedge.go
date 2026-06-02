package dataplane

import (
	"sort"
	"sync"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

type hedgeLatencyTable struct {
	mu     sync.Mutex
	size   int
	values map[core.BackendID]*latencyRing
}

type latencyRing struct {
	values []float64
	next   int
	full   bool
}

func newHedgeLatencyTable(size int) *hedgeLatencyTable {
	if size <= 0 {
		size = 128
	}
	return &hedgeLatencyTable{
		size:   size,
		values: map[core.BackendID]*latencyRing{},
	}
}

func (t *hedgeLatencyTable) Observe(id core.BackendID, latencyMs float64) {
	if latencyMs <= 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	ring := t.values[id]
	if ring == nil {
		ring = &latencyRing{values: make([]float64, t.size)}
		t.values[id] = ring
	}
	ring.values[ring.next] = latencyMs
	ring.next++
	if ring.next >= len(ring.values) {
		ring.next = 0
		ring.full = true
	}
}

func (t *hedgeLatencyTable) Percentile(id core.BackendID, percentile int) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ring := t.values[id]
	if ring == nil {
		return 0, false
	}

	values := ring.snapshot()
	if len(values) == 0 {
		return 0, false
	}
	if percentile <= 0 {
		percentile = 95
	}
	if percentile > 100 {
		percentile = 100
	}

	sort.Float64s(values)
	index := (percentile*len(values)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return time.Duration(values[index] * float64(time.Millisecond)), true
}

func (r *latencyRing) snapshot() []float64 {
	limit := r.next
	if r.full {
		limit = len(r.values)
	}

	out := make([]float64, 0, limit)
	for i := 0; i < limit; i++ {
		if r.values[i] > 0 {
			out = append(out, r.values[i])
		}
	}
	return out
}
