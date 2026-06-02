package learn

import (
	"sync"
	"testing"
)

type counter struct {
	sum   int
	count int
}

func add(current counter, value int) counter {
	current.sum += value
	current.count++
	return current
}

func feed(values []int) counter {
	core := NewCore(counter{}, add)
	defer core.Close()
	for _, value := range values {
		core.Update(value)
	}
	core.Flush()
	return core.Snapshot()
}

func TestSameOrderGivesSameResult(t *testing.T) {
	values := []int{3, 1, 4, 1, 5, 9, 2, 6}
	first := feed(values)
	second := feed(values)
	if first != second {
		t.Fatalf("same input order gave different results: %+v vs %+v", first, second)
	}
	if first.sum != 31 || first.count != 8 {
		t.Fatalf("unexpected result %+v", first)
	}
}

func TestSnapshotIsLockFreeWhileWriting(t *testing.T) {
	core := NewCore(counter{}, add)
	defer core.Close()
	core.Update(10)
	core.Flush()
	if got := core.Snapshot(); got.sum != 10 || got.count != 1 {
		t.Fatalf("snapshot did not reflect the update: %+v", got)
	}
}

func TestConcurrentUpdatesAreNotLost(t *testing.T) {
	core := NewCore(counter{}, add)
	defer core.Close()
	const writers = 50
	const perWriter = 100
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				core.Update(1)
			}
		}()
	}
	wg.Wait()
	core.Flush()
	got := core.Snapshot()
	if got.count != writers*perWriter || got.sum != writers*perWriter {
		t.Fatalf("expected %d updates, got %+v", writers*perWriter, got)
	}
}
