package harness

import (
	"sort"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
)

type HedgeReplayConfig struct {
	Enabled       bool
	Delay         time.Duration
	MaxExtraCalls int
}

type HedgeReplayReport struct {
	Count              int
	PrimaryP95Ms       float64
	HedgedP95Ms        float64
	MeanPrimaryCostUSD float64
	MeanHedgedCostUSD  float64
	ExtraCallRate      float64
}

func ReplayHedging(trace Trace, primary *mock.Backend, backup *mock.Backend, config HedgeReplayConfig) HedgeReplayReport {
	if config.Delay <= 0 {
		config.Delay = 50 * time.Millisecond
	}
	if config.Enabled && config.MaxExtraCalls <= 0 {
		config.MaxExtraCalls = 1
	}

	primaryLatencies := make([]float64, 0, len(trace.Events))
	hedgedLatencies := make([]float64, 0, len(trace.Events))
	var primaryCost float64
	var hedgedCost float64
	extraCalls := 0
	delayMs := float64(config.Delay / time.Millisecond)

	for _, event := range trace.Events {
		primaryOutcome := primary.Outcome(event.Request, event.Arrival)
		primaryLatency := primaryOutcome.LatencyMs
		hedgedLatency := primaryLatency
		requestHedgedCost := primaryOutcome.CostUSD

		if config.Enabled && config.MaxExtraCalls > 0 && primaryLatency > delayMs {
			backupOutcome := backup.Outcome(event.Request, event.Arrival.Add(config.Delay))
			extraCalls++
			requestHedgedCost += backupOutcome.CostUSD
			backupLatency := delayMs + backupOutcome.LatencyMs
			if primaryOutcome.Errored || (!backupOutcome.Errored && backupLatency < hedgedLatency) {
				hedgedLatency = backupLatency
			}
		}

		primaryLatencies = append(primaryLatencies, primaryLatency)
		hedgedLatencies = append(hedgedLatencies, hedgedLatency)
		primaryCost += primaryOutcome.CostUSD
		hedgedCost += requestHedgedCost
	}

	sort.Float64s(primaryLatencies)
	sort.Float64s(hedgedLatencies)
	count := len(trace.Events)
	if count == 0 {
		return HedgeReplayReport{}
	}

	return HedgeReplayReport{
		Count:              count,
		PrimaryP95Ms:       percentile(primaryLatencies, 95),
		HedgedP95Ms:        percentile(hedgedLatencies, 95),
		MeanPrimaryCostUSD: primaryCost / float64(count),
		MeanHedgedCostUSD:  hedgedCost / float64(count),
		ExtraCallRate:      float64(extraCalls) / float64(count),
	}
}
