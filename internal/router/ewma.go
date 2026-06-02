package router

import (
	"math"
	"sync/atomic"

	"github.com/vanshamara/Augur/internal/core"
)

type atomicFloat struct {
	bits atomic.Uint64
}

func newAtomicFloat(value float64) *atomicFloat {
	f := &atomicFloat{}
	f.bits.Store(math.Float64bits(value))
	return f
}

func (f *atomicFloat) load() float64 {
	return math.Float64frombits(f.bits.Load())
}

// update folds a new sample into the moving average, or sets it directly the first
// time, when the stored value is still NaN. It retries until the swap wins, so it is
// safe under concurrent calls.
func (f *atomicFloat) update(sample, alpha float64) {
	for {
		oldBits := f.bits.Load()
		old := math.Float64frombits(oldBits)
		next := sample
		if !math.IsNaN(old) {
			next = alpha*sample + (1-alpha)*old
		}
		if f.bits.CompareAndSwap(oldBits, math.Float64bits(next)) {
			return
		}
	}
}

// EWMA sends each request to the backend with the lowest recent latency, tracked as
// an exponentially weighted moving average. Backends it has never seen are tried
// first, so every backend gets sampled before it settles on the fastest.
type EWMA struct {
	latency map[core.BackendID]*atomicFloat
	alpha   float64
}

func NewEWMA(ids []core.BackendID, alpha float64) *EWMA {
	latency := make(map[core.BackendID]*atomicFloat, len(ids))
	for _, id := range ids {
		latency[id] = newAtomicFloat(math.NaN())
	}
	return &EWMA{latency: latency, alpha: alpha}
}

func (e *EWMA) Name() string {
	return "ewma"
}

func (e *EWMA) Pick(req core.Request, candidates []core.BackendID) core.BackendID {
	best := candidates[0]
	bestLatency := math.Inf(1)
	for _, id := range candidates {
		latency := e.latency[id].load()
		if math.IsNaN(latency) {
			return id
		}
		if latency < bestLatency {
			best = id
			bestLatency = latency
		}
	}
	return best
}

func (e *EWMA) Observe(choice core.BackendID, resp core.Response) {
	e.latency[choice].update(resp.LatencyMs, e.alpha)
}
