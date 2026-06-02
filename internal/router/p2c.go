package router

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

const defaultP2CWindow = 64

type latencySignal struct {
	ewma    *atomicFloat
	p99     *atomicFloat
	window  int
	mu      sync.Mutex
	samples []float64
}

func newLatencySignal(window int) *latencySignal {
	if window <= 0 {
		window = defaultP2CWindow
	}
	return &latencySignal{
		ewma:   newAtomicFloat(math.NaN()),
		p99:    newAtomicFloat(math.NaN()),
		window: window,
	}
}

func (s *latencySignal) observe(sample float64, alpha float64) {
	s.ewma.update(sample, alpha)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.samples = append(s.samples, sample)
	if len(s.samples) > s.window {
		copy(s.samples, s.samples[len(s.samples)-s.window:])
		s.samples = s.samples[:s.window]
	}

	values := append([]float64(nil), s.samples...)
	sort.Float64s(values)
	rank := (99*len(values)+99)/100 - 1
	s.p99.store(values[rank])
}

func (s *latencySignal) score() float64 {
	ewma := s.ewma.load()
	p99 := s.p99.load()
	if math.IsNaN(ewma) {
		return p99
	}
	if math.IsNaN(p99) {
		return ewma
	}
	return math.Max(ewma, p99)
}

type P2C struct {
	latency map[core.BackendID]*latencySignal
	alpha   float64
	deriver *rng.Deriver
}

func NewP2C(ids []core.BackendID, alpha float64, seed uint64) *P2C {
	return NewP2CWithWindow(ids, alpha, seed, defaultP2CWindow)
}

func NewP2CWithWindow(ids []core.BackendID, alpha float64, seed uint64, window int) *P2C {
	latency := make(map[core.BackendID]*latencySignal, len(ids))
	for _, id := range ids {
		latency[id] = newLatencySignal(window)
	}
	return &P2C{latency: latency, alpha: alpha, deriver: rng.NewDeriver(seed)}
}

func (p *P2C) Name() string {
	return "p2c"
}

func (p *P2C) Pick(ctx context.Context, req core.Request, candidates []core.BackendID) core.BackendID {
	if len(candidates) == 1 {
		return candidates[0]
	}
	gen := p.deriver.Rand(rng.HashKey(req.ID))
	first := gen.IntN(len(candidates))
	second := gen.IntN(len(candidates) - 1)
	if second >= first {
		second++
	}
	return p.lowerLatency(candidates[first], candidates[second])
}

func (p *P2C) lowerLatency(a, b core.BackendID) core.BackendID {
	latencyA := p.latency[a].score()
	latencyB := p.latency[b].score()
	if math.IsNaN(latencyA) {
		return a
	}
	if math.IsNaN(latencyB) {
		return b
	}
	if latencyA <= latencyB {
		return a
	}
	return b
}

func (p *P2C) Observe(ctx context.Context, choice core.BackendID, resp core.Response) {
	p.latency[choice].observe(resp.LatencyMs, p.alpha)
}
