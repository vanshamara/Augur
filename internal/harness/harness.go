package harness

import (
	"container/heap"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

type eventKind int

const (
	kindCompletion eventKind = iota
	kindArrival
)

type event struct {
	at     time.Time
	seq    int
	kind   eventKind
	req    core.Request
	choice core.BackendID
	resp   core.Response
}

type eventQueue []event

func (q eventQueue) Len() int {
	return len(q)
}

func (q eventQueue) Less(i, j int) bool {
	if !q[i].at.Equal(q[j].at) {
		return q[i].at.Before(q[j].at)
	}
	if q[i].seq != q[j].seq {
		return q[i].seq < q[j].seq
	}
	return q[i].kind < q[j].kind
}

func (q eventQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

func (q *eventQueue) Push(x any) {
	*q = append(*q, x.(event))
}

func (q *eventQueue) Pop() any {
	old := *q
	last := len(old) - 1
	item := old[last]
	*q = old[:last]
	return item
}

// Run replays a trace through a router against the backends in virtual time order.
// Arrivals are routed and produce an outcome, and a matching completion is scheduled
// at arrival plus latency so the router sees requests finish in the right order. The
// same trace and backends always give the same report.
func Run(trace Trace, route router.Router, backends []*mock.Backend, clk *clock.Virtual) Report {
	byID := make(map[core.BackendID]*mock.Backend, len(backends))
	ids := make([]core.BackendID, 0, len(backends))
	for _, b := range backends {
		byID[b.ID()] = b
		ids = append(ids, b.ID())
	}

	oracle := NewOracle(backends)
	rec := &recorder{}

	queue := &eventQueue{}
	heap.Init(queue)
	for _, e := range trace.Events {
		heap.Push(queue, event{at: e.Arrival, seq: e.Sequence, kind: kindArrival, req: e.Request})
	}

	for queue.Len() > 0 {
		current := heap.Pop(queue).(event)
		advanceTo(clk, current.at)

		switch current.kind {
		case kindArrival:
			choice := route.Pick(current.req, ids)
			chosen := byID[choice]
			outcome := chosen.Outcome(current.req, current.at)
			rec.record(sample{
				backend:               choice,
				latencyMs:             outcome.LatencyMs,
				costUSD:               outcome.CostUSD,
				quality:               chosen.TrueParams(current.at).Quality,
				errored:               outcome.Errored,
				expectedBestLatencyMs: oracle.ExpectedBestLatency(current.at),
				realizedBestLatencyMs: oracle.RealizedBestLatency(current.req, current.at),
			})
			completionAt := current.at.Add(time.Duration(outcome.LatencyMs * float64(time.Millisecond)))
			heap.Push(queue, event{
				at:     completionAt,
				seq:    current.seq,
				kind:   kindCompletion,
				choice: choice,
				resp:   core.Response{Backend: choice, Outcome: outcome},
			})
		case kindCompletion:
			route.Observe(current.choice, current.resp)
		}
	}

	return rec.report(route.Name())
}

func advanceTo(clk *clock.Virtual, t time.Time) {
	delta := t.Sub(clk.Now())
	if delta > 0 {
		clk.Advance(delta)
	}
}
