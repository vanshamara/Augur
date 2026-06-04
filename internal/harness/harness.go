package harness

import (
	"container/heap"
	"context"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/control"
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
	return RunWithPolicy(trace, route, backends, clk, DefaultComparisonPolicy())
}

func RunWithPolicy(trace Trace, route router.Router, backends []*mock.Backend, clk *clock.Virtual, policy *control.Policy) Report {
	routerName := route.Name()
	defer closeRouter(route)

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
			choice := route.Pick(context.Background(), current.req, ids)
			chosen := byID[choice]
			if chosen == nil {
				rec.record(sample{
					backend:                     choice,
					errored:                     true,
					expectedBestLatencyMs:       oracle.ExpectedBestLatency(current.req, current.at),
					realizedBestLatencyMs:       oracle.RealizedBestLatency(current.req, current.at),
					violatedConstraint:          true,
					comparableObjectiveDecision: false,
					feasibleObjectiveDecision:   false,
				})
				continue
			}
			outcome := chosen.Outcome(current.req, current.at)
			policyRegret := oracle.PolicyRegret(current.req, choice, current.at, policy)
			rec.record(sample{
				backend:                     choice,
				latencyMs:                   outcome.LatencyMs,
				costUSD:                     outcome.CostUSD,
				quality:                     chosen.TrueParamsFor(current.req, current.at).Quality,
				errored:                     outcome.Errored,
				expectedBestLatencyMs:       oracle.ExpectedBestLatency(current.req, current.at),
				realizedBestLatencyMs:       oracle.RealizedBestLatency(current.req, current.at),
				objectiveRegret:             policyRegret.ObjectiveRegret,
				learningCost:                policyRegret.LearningCost,
				violatedConstraint:          policyRegret.ViolatedConstraint,
				comparableObjectiveDecision: policyRegret.Comparable,
				feasibleObjectiveDecision:   policyRegret.ChosenFeasible,
			})
			completionAt := current.at.Add(time.Duration(outcome.LatencyMs * float64(time.Millisecond)))
			heap.Push(queue, event{
				at:     completionAt,
				seq:    current.seq,
				kind:   kindCompletion,
				choice: choice,
				resp:   core.Response{RequestID: current.req.ID, Backend: choice, Outcome: outcome},
			})
		case kindCompletion:
			route.Observe(context.Background(), current.choice, current.resp)
			flushRouter(route)
		}
	}

	return rec.report(routerName)
}

func advanceTo(clk *clock.Virtual, t time.Time) {
	delta := t.Sub(clk.Now())
	if delta > 0 {
		clk.Advance(delta)
	}
}

func flushRouter(route router.Router) {
	flusher, ok := route.(interface{ Flush() })
	if ok {
		flusher.Flush()
	}
}

func closeRouter(route router.Router) {
	closer, ok := route.(interface{ Close() })
	if ok {
		closer.Close()
	}
}
