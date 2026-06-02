package router

import (
	"sync/atomic"

	"github.com/vanshamara/Augur/internal/core"
)

// RoundRobin sends each request to the next backend in turn, looping back to the
// start. The counter is atomic so it is safe under concurrent calls.
type RoundRobin struct {
	next atomic.Uint64
}

func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

func (r *RoundRobin) Name() string {
	return "round-robin"
}

func (r *RoundRobin) Pick(req core.Request, candidates []core.BackendID) core.BackendID {
	index := r.next.Add(1) - 1
	return candidates[index%uint64(len(candidates))]
}

func (r *RoundRobin) Observe(choice core.BackendID, resp core.Response) {
}
