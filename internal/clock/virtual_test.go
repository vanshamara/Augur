package clock

import (
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestVirtualNowOnlyMovesOnAdvance(t *testing.T) {
	v := NewVirtual(base)
	if !v.Now().Equal(base) {
		t.Fatalf("expected start %v, got %v", base, v.Now())
	}
	v.Advance(5 * time.Second)
	want := base.Add(5 * time.Second)
	if !v.Now().Equal(want) {
		t.Fatalf("expected %v after advance, got %v", want, v.Now())
	}
}

func TestVirtualAfterFiresWhenDeadlinePassed(t *testing.T) {
	v := NewVirtual(base)
	fire := v.After(10 * time.Second)
	v.Advance(9 * time.Second)
	select {
	case <-fire:
		t.Fatal("timer fired before its deadline")
	default:
	}
	v.Advance(1 * time.Second)
	select {
	case got := <-fire:
		if !got.Equal(base.Add(10 * time.Second)) {
			t.Fatalf("timer fired with wrong time %v", got)
		}
	default:
		t.Fatal("timer did not fire after its deadline passed")
	}
}

func TestVirtualFiresInDeadlineOrder(t *testing.T) {
	v := NewVirtual(base)
	late := v.After(20 * time.Second)
	early := v.After(10 * time.Second)
	v.Advance(30 * time.Second)
	first := <-early
	second := <-late
	if !first.Before(second) {
		t.Fatalf("expected earlier deadline to fire first, got %v then %v", first, second)
	}
}
