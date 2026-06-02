package harness

import (
	"reflect"
	"testing"
)

func compareStable() RegimeComparison {
	seeds := []uint64{1, 2, 3, 4, 5}
	return Compare(StableRegime(), BaselineFactories(), seeds, 300, start)
}

func TestCompareIsDeterministic(t *testing.T) {
	if !reflect.DeepEqual(compareStable(), compareStable()) {
		t.Fatal("the same regime and seeds should give the same comparison")
	}
}

func TestCompareCoversEveryRouter(t *testing.T) {
	c := compareStable()
	want := map[string]bool{"static": false, "round-robin": false, "least-loaded": false, "ewma": false, "cost-aware": false}
	for _, r := range c.Routers {
		want[r.Router] = true
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("router %s missing from the comparison", name)
		}
	}
}

func TestReferenceHasZeroDiffAgainstItself(t *testing.T) {
	c := compareStable()
	for _, r := range c.Routers {
		if r.Router == c.Reference {
			if r.P95Diff.Mean != 0 || r.P95Diff.CIHalf != 0 {
				t.Fatalf("reference should differ from itself by zero, got %v", r.P95Diff)
			}
		}
	}
}
