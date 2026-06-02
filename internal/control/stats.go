package control

import (
	"sort"
	"sync"

	"github.com/vanshamara/Augur/internal/core"
)

type StatTracker struct {
	mu      sync.Mutex
	window  int
	samples map[core.BackendID][]statSample
}

type statSample struct {
	latencyMs float64
	errored   bool
}

func NewStatTracker(ids []core.BackendID, window int) *StatTracker {
	if window <= 0 {
		window = 128
	}
	samples := make(map[core.BackendID][]statSample, len(ids))
	for _, id := range ids {
		samples[id] = nil
	}
	return &StatTracker{window: window, samples: samples}
}

func (s *StatTracker) Observe(id core.BackendID, resp core.Response) {
	s.mu.Lock()
	defer s.mu.Unlock()

	values := append(s.samples[id], statSample{latencyMs: resp.LatencyMs, errored: resp.Errored})
	if len(values) > s.window {
		copy(values, values[len(values)-s.window:])
		values = values[:s.window]
	}
	s.samples[id] = values
}

func (s *StatTracker) Snapshot() map[core.BackendID]BackendStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[core.BackendID]BackendStats, len(s.samples))
	for id, samples := range s.samples {
		if len(samples) == 0 {
			out[id] = BackendStats{}
			continue
		}
		latencies := make([]float64, len(samples))
		errors := 0
		for i, sample := range samples {
			latencies[i] = sample.latencyMs
			if sample.errored {
				errors++
			}
		}
		sort.Float64s(latencies)
		out[id] = BackendStats{
			P95Ms:        percentile(latencies, 95),
			HasP95:       true,
			ErrorRate:    float64(errors) / float64(len(samples)),
			HasErrorRate: true,
		}
	}
	return out
}

func percentile(sorted []float64, p int) float64 {
	rank := (p*len(sorted)+99)/100 - 1
	if rank < 0 {
		return sorted[0]
	}
	if rank >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[rank]
}
