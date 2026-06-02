package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/router"
)

// RouterFactory builds a fresh router for one run. Routers carry state, so each run
// needs its own.
type RouterFactory func(backends []*mock.Backend) router.Router

const BaselineScopeNote = "scope: LiteLLM and Envoy rows are local router shims over the same mock backends. " +
	"They do not measure proxy overhead, retries, provider integrations, streaming, auth, caching, or Redis rate-limit behavior."

// BaselineFactories returns the baseline routers in a fixed order. Static aims
// at the first backend in the set.
func BaselineFactories() []RouterFactory {
	return []RouterFactory{
		func(b []*mock.Backend) router.Router { return router.NewStatic(idsOf(b)[0]) },
		func(b []*mock.Backend) router.Router { return router.NewRoundRobin() },
		func(b []*mock.Backend) router.Router { return router.NewLiteLLMShuffle(nil, 7001) },
		func(b []*mock.Backend) router.Router { return router.NewEnvoyLeastRequest(idsOf(b), nil, 8001) },
		func(b []*mock.Backend) router.Router { return router.NewLeastLoaded(idsOf(b)) },
		func(b []*mock.Backend) router.Router { return router.NewEWMA(idsOf(b), 0.2) },
		func(b []*mock.Backend) router.Router { return router.NewCostAware(pricesOf(b)) },
	}
}

// RouterStats holds one router's results in one regime, averaged over seeds. P95Diff
// is the paired difference against the reference router, where negative means lower
// p95 than the reference.
type RouterStats struct {
	Router                  string
	P95                     Stat
	P99                     Stat
	Cost                    Stat
	ErrorRate               Stat
	Quality                 Stat
	LatencyRegret           Stat
	ObjectiveRegretFeasible Stat
	ConstraintViolationRate Stat
	CostOfLearning          Stat
	P95Diff                 Stat
}

type RegimeComparison struct {
	Regime    string
	Seeds     int
	Requests  int
	Reference string
	Routers   []RouterStats
}

// Compare runs every router over the regime across all seeds and gathers the per
// seed results. Within a seed every router sees the same trace and the same backend
// draws, so the paired p95 difference against the reference cancels shared luck.
func Compare(regime Regime, factories []RouterFactory, seeds []uint64, requests int, start time.Time) RegimeComparison {
	names := make([]string, len(factories))
	p95 := make([][]float64, len(factories))
	p99 := make([][]float64, len(factories))
	cost := make([][]float64, len(factories))
	errorRate := make([][]float64, len(factories))
	quality := make([][]float64, len(factories))
	regret := make([][]float64, len(factories))
	objectiveRegret := make([][]float64, len(factories))
	violations := make([][]float64, len(factories))
	learningCost := make([][]float64, len(factories))

	for _, seed := range seeds {
		trace := GenerateTrace(seed, requests, start)
		for fi, factory := range factories {
			clk := clock.NewVirtual(start)
			backends := regime.Build(seed, clk, start)
			report := Run(trace, factory(backends), backends, clk)
			names[fi] = report.Router
			p95[fi] = append(p95[fi], report.LatencyP95)
			p99[fi] = append(p99[fi], report.LatencyP99)
			cost[fi] = append(cost[fi], report.MeanCostUSD)
			errorRate[fi] = append(errorRate[fi], report.ErrorRate)
			quality[fi] = append(quality[fi], report.MeanQuality)
			regret[fi] = append(regret[fi], report.LatencyRegretMs)
			objectiveRegret[fi] = append(objectiveRegret[fi], report.ObjectiveRegretFeasible)
			violations[fi] = append(violations[fi], report.ConstraintViolationRate)
			learningCost[fi] = append(learningCost[fi], report.CostOfLearning)
		}
	}

	reference := "round-robin"
	refIndex := indexOf(names, reference)

	routers := make([]RouterStats, len(factories))
	for fi := range factories {
		routers[fi] = RouterStats{
			Router:                  names[fi],
			P95:                     statOf(p95[fi]),
			P99:                     statOf(p99[fi]),
			Cost:                    statOf(cost[fi]),
			ErrorRate:               statOf(errorRate[fi]),
			Quality:                 statOf(quality[fi]),
			LatencyRegret:           statOf(regret[fi]),
			ObjectiveRegretFeasible: statOf(objectiveRegret[fi]),
			ConstraintViolationRate: statOf(violations[fi]),
			CostOfLearning:          statOf(learningCost[fi]),
			P95Diff:                 statOf(pairedDiff(p95[fi], p95[refIndex])),
		}
	}

	return RegimeComparison{
		Regime:    regime.Name,
		Seeds:     len(seeds),
		Requests:  requests,
		Reference: reference,
		Routers:   routers,
	}
}

func indexOf(names []string, target string) int {
	for i, name := range names {
		if name == target {
			return i
		}
	}
	return 0
}

// String renders the regime as a table. Each cell is the mean with the 95 percent
// confidence half width.
func (c RegimeComparison) String() string {
	var b strings.Builder
	routerWidth := c.routerColumnWidth()
	fmt.Fprintf(&b, "regime=%s  seeds=%d  requests=%d  reference=%s\n", c.Regime, c.Seeds, c.Requests, c.Reference)
	fmt.Fprintf(&b, "%-*s %-15s %-15s %-13s %-12s %-9s %-15s %-12s %-15s %-15s\n",
		routerWidth,
		"router", "p95 ms", "p99 ms", "cost $", "err", "quality", "obj regret", "viol", "learn cost", "p95 vs ref ms")
	for _, r := range c.Routers {
		fmt.Fprintf(&b, "%-*s %-15s %-15s %-13s %-12s %-9s %-15s %-12s %-15s %-15s\n",
			routerWidth,
			r.Router,
			cell(r.P95.Mean, r.P95.CIHalf, 0),
			cell(r.P99.Mean, r.P99.CIHalf, 0),
			cell(r.Cost.Mean*1e6, r.Cost.CIHalf*1e6, 2),
			cell(r.ErrorRate.Mean, r.ErrorRate.CIHalf, 3),
			cell(r.Quality.Mean, r.Quality.CIHalf, 3),
			cell(r.ObjectiveRegretFeasible.Mean, r.ObjectiveRegretFeasible.CIHalf, 0),
			cell(r.ConstraintViolationRate.Mean, r.ConstraintViolationRate.CIHalf, 3),
			cell(r.CostOfLearning.Mean, r.CostOfLearning.CIHalf, 0),
			cell(r.P95Diff.Mean, r.P95Diff.CIHalf, 0),
		)
	}
	b.WriteString("cost is shown in millionths of a dollar per request\n")
	b.WriteString("obj regret is policy-relative over feasible choices; learn cost is cumulative objective regret\n")
	return b.String()
}

func (c RegimeComparison) routerColumnWidth() int {
	width := len("router")
	for _, r := range c.Routers {
		if len(r.Router) > width {
			width = len(r.Router)
		}
	}
	if width < 13 {
		return 13
	}
	return width
}

func cell(mean, half float64, places int) string {
	format := fmt.Sprintf("%%.%df±%%.%df", places, places)
	return fmt.Sprintf(format, mean, half)
}
