package harness

import (
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/rng"
)

func TestHedgeReplayShowsWhenHedgingHelps(t *testing.T) {
	clk := clock.NewVirtual(start)
	deriver := rng.NewDeriver(11)
	primary := mock.New("slow", mock.SlowStable(), start, deriver, clk)
	backup := mock.New("fast", mock.FastFlaky(), start, deriver, clk)
	trace := GenerateTrace(11, 500, start)

	report := ReplayHedging(trace, primary, backup, HedgeReplayConfig{
		Enabled:       true,
		Delay:         400 * time.Millisecond,
		MaxExtraCalls: 1,
	})

	if report.HedgedP95Ms >= report.PrimaryP95Ms {
		t.Fatalf("hedging should lower p95, got primary %.1f hedged %.1f", report.PrimaryP95Ms, report.HedgedP95Ms)
	}
	if report.ExtraCallRate == 0 {
		t.Fatal("helpful hedging should make extra calls")
	}
}

func TestHedgeReplayShowsWhenHedgingHurts(t *testing.T) {
	clk := clock.NewVirtual(start)
	deriver := rng.NewDeriver(12)
	primary := mock.New("fast", mock.FastFlaky(), start, deriver, clk)
	backup := mock.New("slow", mock.SlowStable(), start, deriver, clk)
	trace := GenerateTrace(12, 500, start)

	report := ReplayHedging(trace, primary, backup, HedgeReplayConfig{
		Enabled:       true,
		Delay:         50 * time.Millisecond,
		MaxExtraCalls: 1,
	})

	if report.MeanHedgedCostUSD <= report.MeanPrimaryCostUSD {
		t.Fatalf("hedging should raise cost, got primary %.8f hedged %.8f", report.MeanPrimaryCostUSD, report.MeanHedgedCostUSD)
	}
	if report.HedgedP95Ms < report.PrimaryP95Ms {
		t.Fatalf("slow backup should not lower p95, got primary %.1f hedged %.1f", report.PrimaryP95Ms, report.HedgedP95Ms)
	}
}
