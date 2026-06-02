package router

import (
	"sync/atomic"

	"github.com/vanshamara/Augur/internal/core"
)

// LeastLoaded sends each request to the backend with the fewest requests in flight.
// It counts a request as in flight from the moment it is picked until Observe reports
// it finished. The per backend counters are atomic and created up front, so the read
// path takes no lock.
type LeastLoaded struct {
	inFlight map[core.BackendID]*atomic.Int64
}

func NewLeastLoaded(ids []core.BackendID) *LeastLoaded {
	inFlight := make(map[core.BackendID]*atomic.Int64, len(ids))
	for _, id := range ids {
		inFlight[id] = &atomic.Int64{}
	}
	return &LeastLoaded{inFlight: inFlight}
}

func (l *LeastLoaded) Name() string {
	return "least-loaded"
}

func (l *LeastLoaded) Pick(req core.Request, candidates []core.BackendID) core.BackendID {
	best := candidates[0]
	bestLoad := l.inFlight[best].Load()
	for _, id := range candidates[1:] {
		load := l.inFlight[id].Load()
		if load < bestLoad {
			best = id
			bestLoad = load
		}
	}
	l.inFlight[best].Add(1)
	return best
}

func (l *LeastLoaded) Observe(choice core.BackendID, resp core.Response) {
	l.inFlight[choice].Add(-1)
}
