package router

import (
	"context"
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

func TestP2CPicksLowerLatencyOfTwo(t *testing.T) {
	ids := []core.BackendID{"a", "b"}
	p := NewP2C(ids, 0.5, 1)
	p.Observe(context.Background(), "a", resp("a", 1000))
	p.Observe(context.Background(), "b", resp("b", 100))
	if got := p.Pick(context.Background(), req, ids); got != "b" {
		t.Fatalf("with two candidates it should keep the faster one, got %s", got)
	}
}

func TestP2CExploresUnseen(t *testing.T) {
	ids := []core.BackendID{"a", "b"}
	p := NewP2C(ids, 0.5, 1)
	p.Observe(context.Background(), "a", resp("a", 100))
	if got := p.Pick(context.Background(), req, ids); got != "b" {
		t.Fatalf("an unseen backend should be tried, got %s", got)
	}
}

func TestP2CAvoidsHighTailLatency(t *testing.T) {
	ids := []core.BackendID{"a", "b"}
	p := NewP2CWithWindow(ids, 0.5, 1, 4)
	p.Observe(context.Background(), "a", resp("a", 100))
	p.Observe(context.Background(), "a", resp("a", 2000))
	p.Observe(context.Background(), "b", resp("b", 300))
	if got := p.Pick(context.Background(), req, ids); got != "b" {
		t.Fatalf("a high p99 signal should lose to a steadier backend, got %s", got)
	}
}

func TestP2CSameRequestPicksSamePair(t *testing.T) {
	ids := []core.BackendID{"a", "b", "c", "d", "e"}
	p := NewP2C(ids, 0.5, 7)
	r := core.Request{ID: "req-42"}
	first := p.Pick(context.Background(), r, ids)
	second := p.Pick(context.Background(), r, ids)
	if first != second {
		t.Fatalf("the same request id should always resolve the same way, got %s then %s", first, second)
	}
}

func TestP2CSingleCandidate(t *testing.T) {
	p := NewP2C([]core.BackendID{"only"}, 0.5, 1)
	if got := p.Pick(context.Background(), req, []core.BackendID{"only"}); got != "only" {
		t.Fatalf("with one candidate it must return that one, got %s", got)
	}
}
