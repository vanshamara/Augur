package control

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/observability"
)

func TestBanditLogsDecisionPropensities(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	bandit := NewBanditRouter(BanditConfig{
		Policy: NewPolicy(PolicyConfig{
			Exploration: ExplorationConfig{JudgeSampleRate: 0.10},
		}),
		Backends: []core.BackendID{"a", "b"},
		Clock:    clk,
		Seed:     1,
	})
	defer bandit.Close()

	req := request("req-1")
	choice := bandit.Pick(context.Background(), req, []core.BackendID{"a", "b"})
	record, ok := bandit.Attribution().Decision(req.ID)
	if !ok {
		t.Fatal("decision should be recorded")
	}
	if record.Backend != choice {
		t.Fatalf("recorded backend %s did not match choice %s", record.Backend, choice)
	}
	if record.RoutingPropensity != 0.5 {
		t.Fatalf("routing propensity got %v want 0.5", record.RoutingPropensity)
	}
	if record.JudgingPropensity != 0.10 {
		t.Fatalf("judging propensity got %v want 0.10", record.JudgingPropensity)
	}
}

func TestBanditRewardUpdateJoinsDecisionByRequestID(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	bandit := NewBanditRouter(BanditConfig{
		Backends: []core.BackendID{"a"},
		Clock:    clk,
		Seed:     1,
	})
	defer bandit.Close()

	req := request("req-1")
	choice := bandit.Pick(context.Background(), req, []core.BackendID{"a"})
	bandit.Observe(context.Background(), choice, core.Response{
		RequestID: req.ID,
		Backend:   choice,
		Outcome:   core.Outcome{LatencyMs: 100},
	})
	bandit.Flush()

	prediction := bandit.RewardModel().Predict("a", EncodeFeatures(req), start)
	if prediction.Mean >= 0 {
		t.Fatalf("latency reward should train a negative reward, got %v", prediction.Mean)
	}
}

func TestBanditDelayedQualityLabelUsesDecisionTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	bandit := NewBanditRouter(BanditConfig{
		Policy: NewPolicy(PolicyConfig{
			Exploration: ExplorationConfig{JudgeSampleRate: 1},
		}),
		Backends: []core.BackendID{"a"},
		Clock:    clk,
		Tau:      time.Second,
		Seed:     1,
	})
	defer bandit.Close()

	req := request("req-1")
	bandit.Pick(context.Background(), req, []core.BackendID{"a"})
	clk.Advance(10 * time.Second)
	if !bandit.ObserveQuality(req.ID, 0) {
		t.Fatal("quality label should join to the decision")
	}
	bandit.Flush()

	prediction := bandit.QualityModel().Predict("a", req, clk.Now())
	if prediction.Count >= 0.001 {
		t.Fatalf("late quality label should be almost fully decayed, count=%v", prediction.Count)
	}
	if prediction.Mean < 0.80 {
		t.Fatalf("stale low label should not erase the optimistic prior, mean=%v", prediction.Mean)
	}
}

func TestBanditShadowTrafficUpdatesCounterfactualArm(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	bandit := NewBanditRouter(BanditConfig{
		Backends: []core.BackendID{"a", "b"},
		Clock:    clk,
		Seed:     1,
		Shadow: func(req core.Request, id core.BackendID, at time.Time) (core.Response, float64, bool) {
			return core.Response{
				RequestID: req.ID,
				Backend:   id,
				Outcome:   core.Outcome{LatencyMs: 10},
			}, 0.95, true
		},
	})
	defer bandit.Close()

	req := request("req-1")
	chosen := bandit.Pick(context.Background(), req, []core.BackendID{"a", "b"})
	record, ok := bandit.Attribution().Decision(req.ID)
	if !ok {
		t.Fatal("decision should be recorded")
	}
	if len(record.ShadowBackends) != 1 {
		t.Fatalf("expected one shadow backend, got %v", record.ShadowBackends)
	}
	shadow := record.ShadowBackends[0]
	if shadow == chosen {
		t.Fatalf("shadow backend should not be the chosen backend")
	}

	bandit.Flush()
	reward := bandit.RewardModel().Predict(shadow, EncodeFeatures(req), start)
	quality := bandit.QualityModel().Predict(shadow, req, start)
	if reward.Count == 0 {
		t.Fatal("shadow reward should update the counterfactual arm")
	}
	if quality.Count == 0 {
		t.Fatal("shadow quality should update the counterfactual arm")
	}
}

func TestRollbackGuardFlagsCanaryRegression(t *testing.T) {
	guard := NewRollbackGuard(RollbackConfig{MinQuality: 0.85})
	baseline := SLOSnapshot{P95Ms: 1000, ErrorRate: 0.01, Quality: 0.90}

	if !guard.ShouldRollback(baseline, SLOSnapshot{P95Ms: 1300, ErrorRate: 0.01, Quality: 0.90}) {
		t.Fatal("p95 regression should roll back")
	}
	if !guard.ShouldRollback(baseline, SLOSnapshot{P95Ms: 1000, ErrorRate: 0.05, Quality: 0.90}) {
		t.Fatal("high error rate should roll back")
	}
	if !guard.ShouldRollback(baseline, SLOSnapshot{P95Ms: 1000, ErrorRate: 0.01, Quality: 0.80}) {
		t.Fatal("quality drop should roll back")
	}
}

func TestBanditEmitsTelemetry(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	recorder := tracetest.NewSpanRecorder()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	bandit := NewBanditRouter(BanditConfig{
		Policy: NewPolicy(PolicyConfig{
			Exploration: ExplorationConfig{JudgeSampleRate: 1},
		}),
		Backends: []core.BackendID{"a"},
		Clock:    clk,
		Seed:     1,
		Observer: observability.New(observability.Config{
			Name:           "test",
			TracerProvider: traces,
		}),
	})
	defer bandit.Close()

	ctx, span := bandit.observer.Start(context.Background(), "request")
	req := request("req-telemetry")
	choice := bandit.Pick(ctx, req, []core.BackendID{"a"})
	bandit.Observe(ctx, choice, core.Response{
		RequestID: req.ID,
		Backend:   choice,
		Outcome:   core.Outcome{LatencyMs: 100},
	})
	if !bandit.ObserveQualityWithContext(ctx, req.ID, 0.9) {
		t.Fatal("quality label should join")
	}
	span.End()

	names := controlSpanNames(recorder.Ended())
	if !names["bandit.pick"] {
		t.Fatal("bandit pick span was not emitted")
	}
	if !names["bandit.reward_update"] {
		t.Fatal("bandit reward span was not emitted")
	}
	if !names["quality.score"] {
		t.Fatal("quality score span was not emitted")
	}
}

func controlSpanNames(spans []sdktrace.ReadOnlySpan) map[string]bool {
	names := map[string]bool{}
	for _, span := range spans {
		names[span.Name()] = true
	}
	return names
}
