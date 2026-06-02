package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/vanshamara/Augur/internal/core"
)

func TestObserverRecordsSpansAndMetrics(t *testing.T) {
	ctx := context.Background()
	recorder := tracetest.NewSpanRecorder()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := metric.NewManualReader()
	metrics := metric.NewMeterProvider(metric.WithReader(reader))
	observer := New(Config{
		Name:           "test",
		TracerProvider: traces,
		MeterProvider:  metrics,
	})

	ctx, span := observer.Start(ctx, "test.span")
	resp := core.Response{
		RequestID: "req-1",
		Backend:   "model-a",
		Outcome: core.Outcome{
			LatencyMs: 50,
			CostUSD:   0.01,
		},
	}
	observer.RecordRoute(ctx, "policy", "bandit", "req-1", "model-a", 2)
	observer.RecordResponse(ctx, resp, nil)
	observer.RecordReward(ctx, "req-1", "model-a", -50)
	observer.RecordQuality(ctx, "req-1", "model-a", 0.9)
	span.End()

	if len(recorder.Ended()) != 1 {
		t.Fatalf("expected one ended span, got %d", len(recorder.Ended()))
	}
	events := recorder.Ended()[0].Events()
	if len(events) < 3 {
		t.Fatalf("expected route, reward, and quality events, got %d", len(events))
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	names := metricNames(data)
	for _, name := range []string{"augur.requests", "augur.routes", "augur.latency_ms", "augur.cost_usd", "augur.reward", "augur.quality_score"} {
		if !names[name] {
			t.Fatalf("missing metric %s in %v", name, names)
		}
	}
}

func metricNames(data metricdata.ResourceMetrics) map[string]bool {
	names := map[string]bool{}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			names[metric.Name] = true
		}
	}
	return names
}
