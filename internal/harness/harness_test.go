package harness

import (
	"reflect"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
	"github.com/vanshamara/Augur/internal/router"
)

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func buildBackends(seed uint64, clk *clock.Virtual) []*mock.Backend {
	deriver := rng.NewDeriver(seed)
	return []*mock.Backend{
		mock.New("cheap", mock.CheapLowerQuality(), start, deriver, clk),
		mock.New("fast", mock.FastFlaky(), start, deriver, clk),
		mock.New("stable", mock.SlowStable(), start, deriver, clk),
	}
}

func runOnce(seed uint64) Report {
	clk := clock.NewVirtual(start)
	backends := buildBackends(seed, clk)
	trace := GenerateTrace(seed, 500, start)
	return Run(trace, router.NewStatic("fast"), backends, clk)
}

func TestRunIsDeterministic(t *testing.T) {
	first := runOnce(123)
	second := runOnce(123)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same trace and seed gave different reports:\n%v\n%v", first, second)
	}
}

func TestReportNumbersAreSane(t *testing.T) {
	report := runOnce(123)
	if report.Count != 500 {
		t.Fatalf("expected 500 requests, got %d", report.Count)
	}
	if report.ErrorRate < 0 || report.ErrorRate > 1 {
		t.Fatalf("error rate out of range: %v", report.ErrorRate)
	}
	total := 0
	for _, n := range report.BackendMix {
		total += n
	}
	if total != report.Count {
		t.Fatalf("backend mix sums to %d, expected %d", total, report.Count)
	}
}

func TestStaticRoutesEverythingToTarget(t *testing.T) {
	report := runOnce(123)
	if report.BackendMix["fast"] != report.Count {
		t.Fatalf("static router should send all %d requests to fast, mix was %v", report.Count, report.BackendMix)
	}
}

func TestPercentileOrdersByRank(t *testing.T) {
	sorted := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentile(sorted, 50); got != 50 {
		t.Fatalf("p50 expected 50, got %v", got)
	}
	if got := percentile(sorted, 99); got != 100 {
		t.Fatalf("p99 expected 100, got %v", got)
	}
}

func TestOracleSingleBackend(t *testing.T) {
	clk := clock.NewVirtual(start)
	only := mock.New("only", mock.SlowStable(), start, rng.NewDeriver(1), clk)
	oracle := NewOracle([]*mock.Backend{only})
	req := core.Request{ID: "req-1", Features: core.Features{PromptTokens: 100, Type: core.Chat}}
	if oracle.ExpectedBestLatency(start) != only.TrueParams(start).MeanLatencyMs {
		t.Fatal("with one backend the expected best should equal its true mean")
	}
	if oracle.RealizedBestLatency(req, start) != only.Outcome(req, start).LatencyMs {
		t.Fatal("with one backend the realized best should equal its own outcome")
	}
}
