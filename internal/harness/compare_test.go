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
	want := map[string]bool{
		"static":              false,
		"round-robin":         false,
		"litellm-shuffle":     false,
		"envoy-least-request": false,
		"least-loaded":        false,
		"ewma":                false,
		"cost-aware":          false,
		"bandit":              false,
	}
	for _, r := range c.Routers {
		want[r.Router] = true
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("router %s missing from the comparison", name)
		}
	}
}

func TestBanditLearnsRequestShapeRegime(t *testing.T) {
	seeds := []uint64{1, 2, 3, 4, 5}
	comparison := Compare(RequestShapeRegime(), BaselineFactories(), seeds, 1200, start)
	stats := routerStatsByName(comparison)

	bandit := stats["bandit"]
	costAware := stats["cost-aware"]
	ewma := stats["ewma"]

	if bandit.ObjectiveRegretFeasible.Mean >= costAware.ObjectiveRegretFeasible.Mean {
		t.Fatalf("bandit should beat cost-aware objective regret in request-shape regime: bandit=%v cost-aware=%v", bandit.ObjectiveRegretFeasible, costAware.ObjectiveRegretFeasible)
	}
	if bandit.ObjectiveRegretFeasible.Mean >= ewma.ObjectiveRegretFeasible.Mean {
		t.Fatalf("bandit should beat ewma objective regret in request-shape regime: bandit=%v ewma=%v", bandit.ObjectiveRegretFeasible, ewma.ObjectiveRegretFeasible)
	}
	if bandit.P95.Mean >= costAware.P95.Mean {
		t.Fatalf("bandit should beat cost-aware p95 in request-shape regime: bandit=%v cost-aware=%v", bandit.P95, costAware.P95)
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

func routerStatsByName(comparison RegimeComparison) map[string]RouterStats {
	out := make(map[string]RouterStats, len(comparison.Routers))
	for _, stats := range comparison.Routers {
		out[stats.Router] = stats
	}
	return out
}
