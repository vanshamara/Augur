package observability

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/vanshamara/Augur/internal/core"
)

type Config struct {
	Name           string
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
}

type Observer struct {
	tracer       trace.Tracer
	requests     metric.Int64Counter
	errors       metric.Int64Counter
	routes       metric.Int64Counter
	latencyMs    metric.Float64Histogram
	costUSD      metric.Float64Counter
	qualityScore metric.Float64Histogram
	rewardValue  metric.Float64Histogram
}

func New(config Config) *Observer {
	if config.Name == "" {
		config.Name = "augur"
	}
	if config.TracerProvider == nil {
		config.TracerProvider = otel.GetTracerProvider()
	}
	if config.MeterProvider == nil {
		config.MeterProvider = otel.GetMeterProvider()
	}

	meter := config.MeterProvider.Meter(config.Name)
	requests, _ := meter.Int64Counter("augur.requests")
	errors, _ := meter.Int64Counter("augur.errors")
	routes, _ := meter.Int64Counter("augur.routes")
	latencyMs, _ := meter.Float64Histogram("augur.latency_ms")
	costUSD, _ := meter.Float64Counter("augur.cost_usd")
	qualityScore, _ := meter.Float64Histogram("augur.quality_score")
	rewardValue, _ := meter.Float64Histogram("augur.reward")

	return &Observer{
		tracer:       config.TracerProvider.Tracer(config.Name),
		requests:     requests,
		errors:       errors,
		routes:       routes,
		latencyMs:    latencyMs,
		costUSD:      costUSD,
		qualityScore: qualityScore,
		rewardValue:  rewardValue,
	}
}

func Noop() *Observer {
	return New(Config{})
}

func (o *Observer) Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if o == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return o.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

func (o *Observer) RecordRoute(ctx context.Context, policyID string, strategy string, requestID string, backend core.BackendID, candidates int) {
	if o == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("policy.id", policyID),
		attribute.String("router.strategy", strategy),
		attribute.String("request.id", requestID),
		attribute.String("backend.id", string(backend)),
		attribute.Int("candidate.count", candidates),
	}
	o.routes.Add(ctx, 1, metric.WithAttributes(attrs...))
	trace.SpanFromContext(ctx).AddEvent("route.selected", trace.WithAttributes(attrs...))
}

func (o *Observer) RecordResponse(ctx context.Context, resp core.Response, err error) {
	if o == nil {
		return
	}
	attrs := responseAttrs(resp)
	o.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
	if resp.LatencyMs > 0 {
		o.latencyMs.Record(ctx, resp.LatencyMs, metric.WithAttributes(attrs...))
	}
	if resp.CostUSD > 0 {
		o.costUSD.Add(ctx, resp.CostUSD, metric.WithAttributes(attrs...))
	}
	if err != nil || resp.Errored {
		o.errors.Add(ctx, 1, metric.WithAttributes(attrs...))
		recordError(ctx, err, resp)
	}
}

func (o *Observer) RecordReward(ctx context.Context, requestID string, backend core.BackendID, reward float64) {
	if o == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("request.id", requestID),
		attribute.String("backend.id", string(backend)),
	}
	o.rewardValue.Record(ctx, reward, metric.WithAttributes(attrs...))
	trace.SpanFromContext(ctx).AddEvent("bandit.reward_update", trace.WithAttributes(append(attrs, attribute.Float64("reward", reward))...))
}

func (o *Observer) RecordQuality(ctx context.Context, requestID string, backend core.BackendID, score float64) {
	if o == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("request.id", requestID),
		attribute.String("backend.id", string(backend)),
		attribute.Float64("quality.score", score),
	}
	o.qualityScore.Record(ctx, score, metric.WithAttributes(attrs...))
	trace.SpanFromContext(ctx).AddEvent("quality.score", trace.WithAttributes(attrs...))
}

func responseAttrs(resp core.Response) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("request.id", resp.RequestID),
		attribute.String("route.name", resp.RouteName),
		attribute.String("backend.id", string(resp.Backend)),
		attribute.Bool("response.errored", resp.Errored),
	}
}

func recordError(ctx context.Context, err error, resp core.Response) {
	span := trace.SpanFromContext(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}
	if resp.Errored {
		err = errors.New("backend returned an error outcome")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}
