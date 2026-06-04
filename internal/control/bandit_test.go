package control

import (
	"context"
	"fmt"
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

func TestBanditLearnsDifferentBackendsByRequestShape(t *testing.T) {
	simple := shapedRequest(core.Features{
		PromptTokens:    200,
		Type:            core.Chat,
		LatencyBudgetMs: 1600,
		CostBudget:      0.05,
		UserTier:        "standard",
	})
	reasoning := shapedRequest(core.Features{
		PromptTokens:    1400,
		Type:            core.Reasoning,
		LatencyBudgetMs: 3000,
		CostBudget:      0.08,
		UserTier:        "premium",
	})
	latencyBound := shapedRequest(core.Features{
		PromptTokens:    300,
		Type:            core.Chat,
		LatencyBudgetMs: 250,
		CostBudget:      0.05,
		UserTier:        "standard",
	})
	costBound := shapedRequest(core.Features{
		PromptTokens:    300,
		Type:            core.Chat,
		LatencyBudgetMs: 2000,
		CostBudget:      0.001,
		UserTier:        "free",
	})

	t.Run("simple request prefers cheap", func(t *testing.T) {
		bandit, ids, outcomes := requestShapeBandit(t)
		defer bandit.Close()
		trainBanditProfile(t, bandit, ids, outcomes, "simple", simple, map[core.BackendID]core.Outcome{
			"cheap":  outcome(100, 0.00005),
			"fast":   outcome(400, 0.00020),
			"strong": outcome(800, 0.00050),
		})
		wantBestBackend(t, bandit, ids, simple, "cheap")
	})

	t.Run("reasoning request prefers strong", func(t *testing.T) {
		bandit, ids, outcomes := requestShapeBandit(t)
		defer bandit.Close()
		trainBanditProfile(t, bandit, ids, outcomes, "simple", simple, map[core.BackendID]core.Outcome{
			"cheap":  outcome(100, 0.00005),
			"fast":   outcome(400, 0.00020),
			"strong": outcome(800, 0.00050),
		})
		trainBanditProfile(t, bandit, ids, outcomes, "reasoning", reasoning, map[core.BackendID]core.Outcome{
			"cheap":  outcome(8000, 0.01000),
			"fast":   outcome(5000, 0.00500),
			"strong": outcome(1, 0.00001),
		})
		wantBestBackend(t, bandit, ids, reasoning, "strong")
	})

	t.Run("latency bound request prefers fast", func(t *testing.T) {
		bandit, ids, outcomes := requestShapeBandit(t)
		defer bandit.Close()
		trainBanditProfile(t, bandit, ids, outcomes, "relaxed", simple, map[core.BackendID]core.Outcome{
			"cheap":  outcome(100, 0.00005),
			"fast":   outcome(300, 0.00020),
			"strong": outcome(600, 0.00050),
		})
		trainBanditProfile(t, bandit, ids, outcomes, "latency", latencyBound, map[core.BackendID]core.Outcome{
			"cheap":  outcome(2000, 0.00005),
			"fast":   outcome(5, 0.00010),
			"strong": outcome(1000, 0.00050),
		})
		wantBestBackend(t, bandit, ids, latencyBound, "fast")
	})

	t.Run("cost bound request prefers cheap", func(t *testing.T) {
		bandit, ids, outcomes := requestShapeBandit(t)
		defer bandit.Close()
		trainBanditProfile(t, bandit, ids, outcomes, "normal", simple, map[core.BackendID]core.Outcome{
			"cheap":  outcome(400, 0.00005),
			"fast":   outcome(100, 0.00020),
			"strong": outcome(80, 0.00050),
		})
		trainBanditProfile(t, bandit, ids, outcomes, "cost", costBound, map[core.BackendID]core.Outcome{
			"cheap":  outcome(100, 0.00001),
			"fast":   outcome(100, 0.01000),
			"strong": outcome(100, 0.02000),
		})
		wantBestBackend(t, bandit, ids, costBound, "cheap")
	})
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

func TestRollbackGuardUsesConfiguredLimits(t *testing.T) {
	guard := NewRollbackGuard(RollbackConfig{
		P95RegressionRatio: 0.50,
		MaxErrorRate:       0.10,
		MinQuality:         0.70,
		MinSamples:         50,
	})
	baseline := SLOSnapshot{Samples: 100, P95Ms: 1000, ErrorRate: 0.01, Quality: 0.90}

	if guard.ShouldRollback(baseline, SLOSnapshot{Samples: 49, P95Ms: 2000, ErrorRate: 0.20, Quality: 0.40}) {
		t.Fatal("low-sample canary should not roll back")
	}
	if guard.ShouldRollback(baseline, SLOSnapshot{Samples: 60, P95Ms: 1400, ErrorRate: 0.09, Quality: 0.75}) {
		t.Fatal("canary inside configured limits should not roll back")
	}
	if !guard.ShouldRollback(baseline, SLOSnapshot{Samples: 60, P95Ms: 1600, ErrorRate: 0.09, Quality: 0.75}) {
		t.Fatal("configured p95 limit should roll back")
	}
	if !guard.ShouldRollback(baseline, SLOSnapshot{Samples: 60, P95Ms: 1000, ErrorRate: 0.11, Quality: 0.75}) {
		t.Fatal("configured error limit should roll back")
	}
	if !guard.ShouldRollback(baseline, SLOSnapshot{Samples: 60, P95Ms: 1000, ErrorRate: 0.09, Quality: 0.69}) {
		t.Fatal("configured quality limit should roll back")
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

func shapedRequest(features core.Features) core.Request {
	return core.Request{Features: features}
}

func requestShapeBandit(t *testing.T) (*BanditRouter, []core.BackendID, *map[core.BackendID]core.Outcome) {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	ids := []core.BackendID{"cheap", "fast", "strong"}
	outcomes := map[core.BackendID]core.Outcome{}
	bandit := NewBanditRouter(BanditConfig{
		Policy: NewPolicy(PolicyConfig{
			Objective: ObjectiveConfig{
				Type:          BlendObjective,
				LatencyWeight: 0.1,
				CostWeight:    1,
			},
		}),
		Backends: ids,
		Clock:    clk,
		Seed:     3,
		Shadow: func(req core.Request, id core.BackendID, at time.Time) (core.Response, float64, bool) {
			return core.Response{
				RequestID: req.ID,
				Backend:   id,
				Outcome:   outcomes[id],
			}, 0.9, true
		},
	})
	return bandit, ids, &outcomes
}

func trainBanditProfile(t *testing.T, bandit *BanditRouter, ids []core.BackendID, current *map[core.BackendID]core.Outcome, label string, template core.Request, profile map[core.BackendID]core.Outcome) {
	t.Helper()
	*current = profile
	for i := 0; i < 80; i++ {
		req := template
		req.ID = fmt.Sprintf("%s-%d", label, i)
		choice := bandit.Pick(context.Background(), req, ids)
		bandit.Observe(context.Background(), choice, core.Response{
			RequestID: req.ID,
			Backend:   choice,
			Outcome:   profile[choice],
		})
		bandit.Flush()
	}
}

func wantBestBackend(t *testing.T, bandit *BanditRouter, ids []core.BackendID, req core.Request, want core.BackendID) {
	t.Helper()
	bandit.Flush()
	snapshot := bandit.RewardModel().Snapshot()
	features := EncodeFeatures(req)
	best := snapshot.BestArm(ids, features, bandit.clock.Now(), bandit.reward.tau, bandit.reward.priorPrecision, bandit.reward.initialMean)
	if best != want {
		for _, id := range ids {
			prediction := snapshot.Predict(id, features, bandit.clock.Now(), bandit.reward.tau, bandit.reward.priorPrecision, bandit.reward.initialMean, false)
			t.Logf("prediction %s mean %.3f count %.1f", id, prediction.Mean, prediction.Count)
		}
		t.Fatalf("best backend got %s want %s", best, want)
	}
}

func outcome(latencyMs float64, costUSD float64) core.Outcome {
	return core.Outcome{LatencyMs: latencyMs, CostUSD: costUSD}
}

func controlSpanNames(spans []sdktrace.ReadOnlySpan) map[string]bool {
	names := map[string]bool{}
	for _, span := range spans {
		names[span.Name()] = true
	}
	return names
}
