package rng

import (
	"sync"
	"testing"
)

func draws(d *Deriver, keys ...uint64) []uint64 {
	r := d.Rand(keys...)
	out := make([]uint64, 8)
	for i := range out {
		out[i] = r.Uint64()
	}
	return out
}

func equal(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSameKeysGiveSameStream(t *testing.T) {
	d := NewDeriver(42)
	if !equal(draws(d, 1, 2), draws(d, 1, 2)) {
		t.Fatal("same seed and keys should give the same stream")
	}
}

func TestDifferentKeysGiveDifferentStream(t *testing.T) {
	d := NewDeriver(42)
	if equal(draws(d, 1, 2), draws(d, 1, 3)) {
		t.Fatal("different keys should give different streams")
	}
}

func TestDifferentSeedGivesDifferentStream(t *testing.T) {
	if equal(draws(NewDeriver(1), 7), draws(NewDeriver(2), 7)) {
		t.Fatal("different seeds should give different streams")
	}
}

func TestStreamDoesNotDependOnCreationOrder(t *testing.T) {
	d := NewDeriver(42)
	first := draws(d, 100)
	_ = draws(d, 200)
	_ = draws(d, 300)
	again := draws(d, 100)
	if !equal(first, again) {
		t.Fatal("the stream for a key must not change based on other generators")
	}
}

func TestConcurrentDerivationIsStable(t *testing.T) {
	d := NewDeriver(42)
	want := draws(d, 5)
	var wg sync.WaitGroup
	results := make([][]uint64, 50)
	for i := range results {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			results[slot] = draws(d, 5)
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if !equal(got, want) {
			t.Fatalf("goroutine %d got a different stream for the same key", i)
		}
	}
}

func TestHashKeyIsStable(t *testing.T) {
	if HashKey("request-abc") != HashKey("request-abc") {
		t.Fatal("HashKey should return the same value for the same string")
	}
	if HashKey("request-abc") == HashKey("request-xyz") {
		t.Fatal("HashKey should differ for different strings")
	}
}
