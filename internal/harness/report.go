package harness

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vanshamara/Augur/internal/core"
)

type sample struct {
	backend               core.BackendID
	latencyMs             float64
	costUSD               float64
	quality               float64
	errored               bool
	expectedBestLatencyMs float64
	realizedBestLatencyMs float64
}

type recorder struct {
	samples []sample
}

func (r *recorder) record(s sample) {
	r.samples = append(r.samples, s)
}

// Report holds the measured results for one router over one trace. Latency regret
// is the chosen latency minus the expectation oracle. The realization gap is a
// separate annotation: how much of the latency was just luck that no router could
// have captured.
type Report struct {
	Router           string
	Count            int
	LatencyP50       float64
	LatencyP95       float64
	LatencyP99       float64
	MeanCostUSD      float64
	ErrorRate        float64
	MeanQuality      float64
	BackendMix       map[core.BackendID]int
	LatencyRegretMs  float64
	RealizationGapMs float64
}

func (r *recorder) report(routerName string) Report {
	count := len(r.samples)
	report := Report{Router: routerName, Count: count, BackendMix: map[core.BackendID]int{}}
	if count == 0 {
		return report
	}

	latencies := make([]float64, count)
	var totalCost, totalQuality, totalRegret, totalGap float64
	errored := 0
	for i, s := range r.samples {
		latencies[i] = s.latencyMs
		totalCost += s.costUSD
		totalQuality += s.quality
		totalRegret += s.latencyMs - s.expectedBestLatencyMs
		totalGap += s.expectedBestLatencyMs - s.realizedBestLatencyMs
		if s.errored {
			errored++
		}
		report.BackendMix[s.backend]++
	}
	sort.Float64s(latencies)

	report.LatencyP50 = percentile(latencies, 50)
	report.LatencyP95 = percentile(latencies, 95)
	report.LatencyP99 = percentile(latencies, 99)
	report.MeanCostUSD = totalCost / float64(count)
	report.MeanQuality = totalQuality / float64(count)
	report.ErrorRate = float64(errored) / float64(count)
	report.LatencyRegretMs = totalRegret / float64(count)
	report.RealizationGapMs = totalGap / float64(count)
	return report
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted)+99)/100 - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// String renders the report as a small readable block, with backend ids sorted so
// the output is stable.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "router=%s requests=%d\n", r.Router, r.Count)
	fmt.Fprintf(&b, "  latency ms      p50=%.1f p95=%.1f p99=%.1f\n", r.LatencyP50, r.LatencyP95, r.LatencyP99)
	fmt.Fprintf(&b, "  mean cost       $%.6f\n", r.MeanCostUSD)
	fmt.Fprintf(&b, "  error rate      %.3f\n", r.ErrorRate)
	fmt.Fprintf(&b, "  mean quality    %.3f\n", r.MeanQuality)
	fmt.Fprintf(&b, "  latency regret  %.1f ms vs expectation oracle\n", r.LatencyRegretMs)
	fmt.Fprintf(&b, "  realization gap %.1f ms (luck floor)\n", r.RealizationGapMs)

	ids := make([]string, 0, len(r.BackendMix))
	for id := range r.BackendMix {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	b.WriteString("  backend mix\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "    %-22s %d\n", id, r.BackendMix[core.BackendID(id)])
	}
	return b.String()
}
