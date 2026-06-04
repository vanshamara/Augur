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

type DecisionRecord struct {
	RequestID         string
	TenantID          string
	RouteName         string
	RequestType       core.RequestType
	PromptTokens      int
	LatencyBudgetMs   int
	CostBudgetUSD     float64
	Candidates        []core.BackendID
	Excluded          []DecisionExclusion
	ReasonSummary     string
	Canary            DecisionCanary
	Selected          core.BackendID
	AttemptedBackends []core.BackendID
	FallbackCount     int
	EstimatedCostUSD  float64
	Error             string
}

type DecisionExclusion struct {
	Backend core.BackendID
	Stage   string
	Reason  string
}

type DecisionCanary struct {
	Configured     bool
	Assigned       bool
	Mode           string
	Backend        core.BackendID
	StickyKeyHash  string
	RollbackReason string
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
	metricAttrs := []attribute.KeyValue{
		attribute.String("policy.id", policyID),
		attribute.String("router.strategy", strategy),
		attribute.String("backend.id", string(backend)),
		attribute.Int("candidate.count", candidates),
	}
	o.routes.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
	eventAttrs := append(metricAttrs, attribute.String("request.id", requestID))
	trace.SpanFromContext(ctx).AddEvent("route.selected", trace.WithAttributes(eventAttrs...))
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

func (o *Observer) RecordDecision(ctx context.Context, record DecisionRecord) {
	if o == nil {
		return
	}
	trace.SpanFromContext(ctx).AddEvent("route.decision", trace.WithAttributes(decisionAttrs(record)...))
}

func (o *Observer) RecordReward(ctx context.Context, requestID string, backend core.BackendID, reward float64) {
	if o == nil {
		return
	}
	o.rewardValue.Record(ctx, reward, metric.WithAttributes(attribute.String("backend.id", string(backend))))
	trace.SpanFromContext(ctx).AddEvent("bandit.reward_update", trace.WithAttributes(
		attribute.String("request.id", requestID),
		attribute.String("backend.id", string(backend)),
		attribute.Float64("reward", reward),
	))
}

func (o *Observer) RecordQuality(ctx context.Context, requestID string, backend core.BackendID, score float64) {
	if o == nil {
		return
	}
	o.qualityScore.Record(ctx, score, metric.WithAttributes(attribute.String("backend.id", string(backend))))
	trace.SpanFromContext(ctx).AddEvent("quality.score", trace.WithAttributes(
		attribute.String("request.id", requestID),
		attribute.String("backend.id", string(backend)),
		attribute.Float64("quality.score", score),
	))
}

// responseAttrs returns low-cardinality labels for response metrics. It leaves
// out the request id on purpose, since per-request labels would create one time
// series per request. The request id stays on trace spans instead.
func responseAttrs(resp core.Response) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("route.name", resp.RouteName),
		attribute.String("backend.id", string(resp.Backend)),
		attribute.String("canary.mode", resp.CanaryMode),
		attribute.String("canary.backend", string(resp.CanaryBackend)),
		attribute.String("canary.rollback_reason", resp.CanaryRollback),
		attribute.Bool("response.errored", resp.Errored),
	}
}

func decisionAttrs(record DecisionRecord) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("request.id", record.RequestID),
		attribute.String("route.name", record.RouteName),
		attribute.String("request.type", string(record.RequestType)),
		attribute.Int("prompt.tokens", record.PromptTokens),
		attribute.StringSlice("candidate.ids", backendIDStrings(record.Candidates)),
		attribute.Bool("canary.configured", record.Canary.Configured),
		attribute.Bool("canary.assigned", record.Canary.Assigned),
	}
	if record.TenantID != "" {
		attrs = append(attrs, attribute.String("tenant.id", record.TenantID))
	}
	if record.LatencyBudgetMs > 0 {
		attrs = append(attrs, attribute.Int("latency.budget_ms", record.LatencyBudgetMs))
	}
	if record.CostBudgetUSD > 0 {
		attrs = append(attrs, attribute.Float64("cost.budget_usd", record.CostBudgetUSD))
	}
	if len(record.Excluded) > 0 {
		attrs = append(attrs, attribute.StringSlice("excluded.backends", exclusionStrings(record.Excluded)))
	}
	if record.ReasonSummary != "" {
		attrs = append(attrs, attribute.String("route.reason_summary", record.ReasonSummary))
	}
	if record.Selected != "" {
		attrs = append(attrs, attribute.String("backend.selected", string(record.Selected)))
	}
	if len(record.AttemptedBackends) > 0 {
		attrs = append(attrs, attribute.StringSlice("backend.attempted", backendIDStrings(record.AttemptedBackends)))
	}
	if record.FallbackCount > 0 {
		attrs = append(attrs, attribute.Int("fallback.count", record.FallbackCount))
	}
	if record.EstimatedCostUSD > 0 {
		attrs = append(attrs, attribute.Float64("cost.estimated_usd", record.EstimatedCostUSD))
	}
	if record.Error != "" {
		attrs = append(attrs, attribute.String("error.message", record.Error))
	}
	return append(attrs, canaryDecisionAttrs(record.Canary)...)
}

func canaryDecisionAttrs(canary DecisionCanary) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}
	if canary.Mode != "" {
		attrs = append(attrs, attribute.String("canary.mode", canary.Mode))
	}
	if canary.Backend != "" {
		attrs = append(attrs, attribute.String("canary.backend", string(canary.Backend)))
	}
	if canary.StickyKeyHash != "" {
		attrs = append(attrs, attribute.String("canary.sticky_key_hash", canary.StickyKeyHash))
	}
	if canary.RollbackReason != "" {
		attrs = append(attrs, attribute.String("canary.rollback_reason", canary.RollbackReason))
	}
	return attrs
}

func backendIDStrings(values []core.BackendID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func exclusionStrings(values []DecisionExclusion) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value.Backend)+":"+value.Stage+":"+value.Reason)
	}
	return out
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
