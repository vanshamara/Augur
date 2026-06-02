package router

import (
	"context"
	"sync"
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

var req = core.Request{ID: "x"}

func resp(id core.BackendID, latencyMs float64) core.Response {
	return core.Response{Backend: id, Outcome: core.Outcome{LatencyMs: latencyMs}}
}

func TestStaticPicksTargetThenFallsBack(t *testing.T) {
	s := NewStatic("b")
	if got := s.Pick(context.Background(), req, []core.BackendID{"a", "b", "c"}); got != "b" {
		t.Fatalf("expected target b, got %s", got)
	}
	if got := s.Pick(context.Background(), req, []core.BackendID{"a", "c"}); got != "a" {
		t.Fatalf("expected fallback to first, got %s", got)
	}
}

func TestRoundRobinCycles(t *testing.T) {
	rr := NewRoundRobin()
	ids := []core.BackendID{"a", "b", "c"}
	want := []core.BackendID{"a", "b", "c", "a", "b", "c"}
	for i, w := range want {
		if got := rr.Pick(context.Background(), req, ids); got != w {
			t.Fatalf("pick %d: got %s want %s", i, got, w)
		}
	}
}

func TestLeastLoadedBalancesByInFlight(t *testing.T) {
	ids := []core.BackendID{"a", "b", "c"}
	ll := NewLeastLoaded(ids)

	expect := func(want core.BackendID) {
		if got := ll.Pick(context.Background(), req, ids); got != want {
			t.Fatalf("expected %s, got %s", want, got)
		}
	}
	expect("a") // all zero, first wins
	expect("b") // a is busy
	expect("c") // a and b busy
	expect("a") // all at one, first wins again

	ll.Observe(context.Background(), "a", resp("a", 100)) // a back to one
	ll.Observe(context.Background(), "a", resp("a", 100)) // a back to zero
	expect("a")                                           // a is now the least loaded
}

func TestEWMAExploresThenPicksLowest(t *testing.T) {
	ids := []core.BackendID{"slow", "fast"}
	e := NewEWMA(ids, 0.5)

	if got := e.Pick(context.Background(), req, ids); got != "slow" {
		t.Fatalf("first pick should explore the first unseen backend, got %s", got)
	}
	e.Observe(context.Background(), "slow", resp("slow", 1000))
	if got := e.Pick(context.Background(), req, ids); got != "fast" {
		t.Fatalf("second pick should explore the still unseen backend, got %s", got)
	}
	e.Observe(context.Background(), "fast", resp("fast", 100))
	if got := e.Pick(context.Background(), req, ids); got != "fast" {
		t.Fatalf("once both are seen it should pick the faster one, got %s", got)
	}
}

func TestCostAwarePicksCheapest(t *testing.T) {
	prices := map[core.BackendID]float64{"pricey": 10, "cheap": 1, "mid": 5}
	c := NewCostAware(prices)
	if got := c.Pick(context.Background(), req, []core.BackendID{"pricey", "cheap", "mid"}); got != "cheap" {
		t.Fatalf("expected cheap, got %s", got)
	}
}

func TestSignalRoutersAreRaceFree(t *testing.T) {
	ids := []core.BackendID{"a", "b", "c"}
	ll := NewLeastLoaded(ids)
	e := NewEWMA(ids, 0.3)

	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				loaded := ll.Pick(context.Background(), req, ids)
				ll.Observe(context.Background(), loaded, resp(loaded, 100))
				fast := e.Pick(context.Background(), req, ids)
				e.Observe(context.Background(), fast, resp(fast, 100))
			}
		}()
	}
	wg.Wait()

	var total int64
	for _, id := range ids {
		total += ll.inFlight[id].Load()
	}
	if total != 0 {
		t.Fatalf("every picked request was observed, so in flight should net to zero, got %d", total)
	}
}
