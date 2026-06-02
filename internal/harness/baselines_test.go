package harness

import (
	"testing"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/router"
)

func TestCostAwareSendsAllToCheapest(t *testing.T) {
	clk := clock.NewVirtual(start)
	backends := buildBackends(7, clk)
	report := Run(GenerateTrace(7, 300, start), router.NewCostAware(pricesOf(backends)), backends, clk)
	if report.BackendMix["cheap"] != report.Count {
		t.Fatalf("cost-aware should send every request to cheap, got %v", report.BackendMix)
	}
}

func TestEWMAFavorsFastest(t *testing.T) {
	clk := clock.NewVirtual(start)
	backends := buildBackends(7, clk)
	report := Run(GenerateTrace(7, 500, start), router.NewEWMA(idsOf(backends), 0.2), backends, clk)
	mix := report.BackendMix
	if mix["fast"] <= mix["cheap"] || mix["fast"] <= mix["stable"] {
		t.Fatalf("ewma should favor the fastest backend, got %v", mix)
	}
}

func TestRoundRobinSpreadsEvenly(t *testing.T) {
	clk := clock.NewVirtual(start)
	backends := buildBackends(7, clk)
	report := Run(GenerateTrace(7, 300, start), router.NewRoundRobin(), backends, clk)
	counts := []int{report.BackendMix["cheap"], report.BackendMix["fast"], report.BackendMix["stable"]}
	low, high := counts[0], counts[0]
	for _, c := range counts {
		if c < low {
			low = c
		}
		if c > high {
			high = c
		}
	}
	if high-low > 1 {
		t.Fatalf("round-robin should spread evenly, got %v", report.BackendMix)
	}
}
