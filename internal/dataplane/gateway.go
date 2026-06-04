package dataplane

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/observability"
	"github.com/vanshamara/Augur/internal/router"
)

var (
	ErrNoCandidates           = errors.New("no backend candidates")
	ErrNoCompatibleCandidates = errors.New("no backend candidates support request type")
	ErrLoadShed               = errors.New("request shed by data plane")
	ErrMissing                = errors.New("backend is not registered")
	ErrStreaming              = errors.New("backend does not support streaming")
	ErrBackendTimeout         = errors.New("backend timeout")
)

type HedgeConfig struct {
	Enabled           bool
	Delay             time.Duration
	MaxInFlight       int64
	BudgetFraction    *float64
	TriggerPercentile int
	MaxExtraCalls     int
}

type Config struct {
	Router          router.Router
	Backends        []backend.Backend
	Routes          []RouteRule
	Capabilities    map[core.BackendID][]core.RequestType
	Canary          CanaryConfig
	Pricing         map[core.BackendID]BackendPrice
	Decisions       *DecisionLog
	BackendTimeouts map[core.BackendID]time.Duration
	ActiveHealth    bool
	Filters         []Filter
	Clock           clock.Clock
	Hedge           HedgeConfig
	SingleFlight    *SingleFlight
	SingleFlightKey KeyFunc
	Observer        *observability.Observer
}

type Gateway struct {
	router          router.Router
	ids             []core.BackendID
	backends        map[core.BackendID]backend.Backend
	routes          *RouteSelector
	capabilities    map[core.BackendID]map[core.RequestType]bool
	canaries        *CanaryTable
	pricing         map[core.BackendID]BackendPrice
	decisions       *DecisionLog
	status          *backendStatusTable
	timeouts        map[core.BackendID]time.Duration
	activeHealth    bool
	filters         []Filter
	clock           clock.Clock
	hedge           HedgeConfig
	hedgesInFlight  atomic.Int64
	hedgeLatencies  *hedgeLatencyTable
	singleFlight    *SingleFlight
	singleFlightKey KeyFunc
	observer        *observability.Observer
}

func New(config Config) (*Gateway, error) {
	if config.Router == nil {
		return nil, errors.New("router is required")
	}
	if len(config.Backends) == 0 {
		return nil, errors.New("at least one backend is required")
	}
	if config.Clock == nil {
		config.Clock = clock.NewReal()
	}
	if config.Hedge.Delay <= 0 {
		config.Hedge.Delay = 50 * time.Millisecond
	}
	if config.Hedge.TriggerPercentile <= 0 {
		config.Hedge.TriggerPercentile = 95
	}
	if config.Hedge.MaxExtraCalls <= 0 {
		config.Hedge.MaxExtraCalls = 1
	}
	if config.Observer == nil {
		config.Observer = observability.Noop()
	}

	ids := make([]core.BackendID, 0, len(config.Backends))
	backends := make(map[core.BackendID]backend.Backend, len(config.Backends))
	for _, b := range config.Backends {
		id := b.ID()
		ids = append(ids, id)
		backends[id] = b
	}
	capabilities := normalizeCapabilities(ids, config.Capabilities)

	return &Gateway{
		router:          config.Router,
		ids:             ids,
		backends:        backends,
		routes:          NewRouteSelector(config.Routes),
		capabilities:    capabilities,
		canaries:        NewCanaryTable(config.Canary),
		pricing:         copyPricing(config.Pricing),
		decisions:       config.Decisions,
		status:          newBackendStatusTable(ids, config.BackendTimeouts),
		timeouts:        copyTimeouts(config.BackendTimeouts),
		activeHealth:    config.ActiveHealth,
		filters:         append([]Filter(nil), config.Filters...),
		clock:           config.Clock,
		hedge:           config.Hedge,
		hedgeLatencies:  newHedgeLatencyTable(128),
		singleFlight:    config.SingleFlight,
		singleFlightKey: config.SingleFlightKey,
		observer:        config.Observer,
	}, nil
}

func (g *Gateway) Ready() bool {
	for _, status := range g.BackendStatus() {
		if status.Healthy && status.CircuitMode != "open" {
			return true
		}
	}
	return false
}

func (g *Gateway) BackendStatus() []BackendStatus {
	out := make([]BackendStatus, 0, len(g.ids))
	for _, id := range g.ids {
		status := g.status.Snapshot(id)
		status.Healthy = true
		status.ActiveHealthConfigured = g.activeHealth
		for _, filter := range g.filters {
			switch typed := filter.(type) {
			case *HealthFilter:
				health := typed.Status(id)
				status.Healthy = health.Healthy
				status.LastHealthCheck = health.LastChecked
				status.HealthError = health.LastError
				status.ConsecutiveFailures = health.ConsecutiveFailures
				status.ConsecutiveSuccesses = health.ConsecutiveSuccesses
			case *CircuitBreaker:
				status.CircuitMode = typed.Mode(id)
			case *AdaptiveLimiter:
				status.ConcurrencyLimit = typed.Limit(id)
				status.ConcurrencyInFlight = typed.InFlight(id)
			}
		}
		out = append(out, status)
	}
	return out
}

// Call routes one request through the filter chain and backend fleet.
func (g *Gateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	ctx, span := g.observer.Start(ctx, "gateway.call",
		attribute.String("request.id", req.ID),
		attribute.String("router.name", g.router.Name()),
	)
	defer span.End()

	if g.singleFlight != nil && g.singleFlightKey != nil {
		key := g.singleFlightKey(req)
		resp, err := g.singleFlight.Do(ctx, key, func() (core.Response, error) {
			return g.callUnique(ctx, req)
		})
		g.recordGatewayError(ctx, resp, err)
		return resp, err
	}
	resp, err := g.callUnique(ctx, req)
	g.recordGatewayError(ctx, resp, err)
	return resp, err
}

func (g *Gateway) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	ctx, span := g.observer.Start(ctx, "gateway.stream",
		attribute.String("request.id", req.ID),
		attribute.String("router.name", g.router.Name()),
	)
	defer span.End()

	candidates := g.candidates(req)
	record := candidates.Record
	if candidates.Err != nil {
		g.storeDecision(record, core.Response{}, candidates.Err)
		return nil, candidates.Err
	}
	if len(candidates.IDs) == 0 && len(candidates.Fallbacks) == 0 {
		g.storeDecision(record, core.Response{}, ErrNoCandidates)
		return nil, ErrNoCandidates
	}
	g.startShadow(ctx, req, candidates)
	if len(candidates.Fallbacks) > 0 {
		stream, err := g.streamWithFallbacks(ctx, req, candidates)
		g.storeStreamDecision(record, stream, err)
		return stream, err
	}

	remaining := copyCandidates(candidates.IDs)
	attempts := []core.BackendID{}
	for len(remaining) > 0 {
		choice := g.router.Pick(ctx, req, remaining)
		if choice == "" {
			g.storeDecision(record, core.Response{}, ErrNoCandidates)
			return nil, ErrNoCandidates
		}
		g.observer.RecordRoute(ctx, "data-plane-stream", g.router.Name(), req.ID, choice, len(remaining))
		attempts = append(attempts, choice)
		stream, err := g.streamBackend(ctx, req, choice, candidates.RouteName, candidates.Canary, attempts, 0)
		if !errors.Is(err, ErrLoadShed) {
			g.storeStreamDecision(record, stream, err)
			return stream, err
		}
		remaining = without(remaining, choice)
	}
	err := newAttemptError(ErrLoadShed, attempts, 0)
	g.storeDecision(record, core.Response{}, err)
	return nil, err
}

func (g *Gateway) callUnique(ctx context.Context, req core.Request) (core.Response, error) {
	candidates := g.candidates(req)
	record := candidates.Record
	if candidates.Err != nil {
		g.storeDecision(record, core.Response{}, candidates.Err)
		return core.Response{}, candidates.Err
	}
	if len(candidates.IDs) == 0 && len(candidates.Fallbacks) == 0 {
		g.storeDecision(record, core.Response{}, ErrNoCandidates)
		return core.Response{}, ErrNoCandidates
	}
	g.startShadow(ctx, req, candidates)
	resp, err := g.dispatch(ctx, req, candidates)
	g.storeDecision(record, resp, err)
	return resp, err
}

func (g *Gateway) dispatch(ctx context.Context, req core.Request, candidates candidateSet) (core.Response, error) {
	if len(candidates.Fallbacks) > 0 {
		return g.callWithFallbacks(ctx, req, candidates)
	}
	if !g.shouldHedge(req, candidates.IDs) {
		return g.callRouted(ctx, req, candidates)
	}
	return g.callHedged(ctx, req, candidates)
}

type candidateSet struct {
	RouteName string
	IDs       []core.BackendID
	Fallbacks []core.BackendID
	Canary    CanaryDecision
	Record    *RouteDecisionRecord
	Err       error
}

func (g *Gateway) candidates(req core.Request) candidateSet {
	decision := g.routes.Select(req, g.ids)
	candidates := copyCandidates(decision.Candidates)
	fallbacks := copyCandidates(decision.Fallbacks)
	canaryCandidates := copyCandidates(routeCandidates([]core.BackendID{decision.Canary.Backend}, g.ids))
	record := g.newDecisionRecord(req, decision)

	if len(candidates) == 0 && len(fallbacks) == 0 {
		return candidateSet{RouteName: decision.Name, Record: record}
	}

	requestType := requestTypeForCapabilities(req)
	compatible := g.compatibleCandidates(req, candidates)
	record.addExclusions("capability", fmt.Sprintf("does not support %s", requestType), dropped(candidates, compatible))
	candidates = compatible
	fallbacks = g.compatibleCandidates(req, fallbacks)
	canaryCandidates = g.compatibleCandidates(req, canaryCandidates)
	if len(candidates) == 0 && len(fallbacks) == 0 && len(canaryCandidates) == 0 {
		err := fmt.Errorf("%w %q", ErrNoCompatibleCandidates, requestType)
		return candidateSet{RouteName: decision.Name, Record: record, Err: err}
	}

	for _, filter := range g.filters {
		kept := filter.Apply(req, candidates)
		record.addExclusions(filter.Name(), filter.Name()+" filter excluded the backend", dropped(candidates, kept))
		candidates = kept
		if len(fallbacks) > 0 {
			fallbacks = filter.Apply(req, fallbacks)
		}
		if len(canaryCandidates) > 0 {
			canaryCandidates = filter.Apply(req, canaryCandidates)
		}
	}

	out := candidateSet{RouteName: decision.Name, IDs: candidates, Fallbacks: fallbacks, Record: record}
	beforeBudget := out.IDs
	out = g.applyBudget(req, out)
	out.Record = record
	record.addExclusions("budget", "estimated cost over budget", dropped(beforeBudget, out.IDs))
	if out.Err != nil {
		return out
	}

	out = g.applyCanary(req, out, decision.Canary, canaryCandidates)
	out.Record = record
	g.recordCanary(record, req, decision.Canary, out.Canary)
	return out
}

func (g *Gateway) newDecisionRecord(req core.Request, decision RouteDecision) *RouteDecisionRecord {
	if g.decisions == nil {
		return nil
	}
	return &RouteDecisionRecord{
		RequestID:       req.ID,
		TenantID:        req.TenantID,
		RouteName:       decision.Name,
		RequestType:     requestTypeForCapabilities(req),
		PromptTokens:    req.Features.PromptTokens,
		LatencyBudgetMs: req.Features.LatencyBudgetMs,
		CostBudgetUSD:   req.Features.CostBudget,
		Candidates:      copyCandidates(decision.Candidates),
	}
}

func (g *Gateway) recordCanary(record *RouteDecisionRecord, req core.Request, rule CanaryRule, decision CanaryDecision) {
	if record == nil {
		return
	}
	record.Canary = CanaryRecord{
		Configured:     rule.Backend != "",
		Assigned:       rule.Backend != "" && canaryAssigned(req, rule),
		Mode:           decision.Mode,
		Backend:        rule.Backend,
		StickyKeyHash:  stickyKeyHash(req, rule),
		RollbackReason: decision.RollbackReason,
	}
}

func (g *Gateway) storeDecision(record *RouteDecisionRecord, resp core.Response, err error) {
	if record == nil {
		return
	}
	record.finish(resp, err)
	g.decisions.put(record)
}

func (g *Gateway) storeStreamDecision(record *RouteDecisionRecord, stream core.Stream, err error) {
	if record == nil {
		return
	}
	resp := core.Response{}
	if stream != nil {
		if meta, ok := stream.(streamCostMetadata); ok {
			resp.Backend = meta.BackendID()
			resp.EstimatedCostUSD = meta.EstimatedCostUSD()
		}
	}
	record.finish(resp, err)
	g.decisions.put(record)
}

// DecisionRecords returns recent routing decisions for the debug endpoint.
func (g *Gateway) DecisionRecords() []RouteDecisionRecord {
	return g.decisions.Recent()
}

// DecisionRecord returns the routing decision for one request id when it is
// still in the decision log.
func (g *Gateway) DecisionRecord(requestID string) (RouteDecisionRecord, bool) {
	return g.decisions.Lookup(requestID)
}

func (g *Gateway) applyCanary(req core.Request, candidates candidateSet, rule CanaryRule, canaryCandidates []core.BackendID) candidateSet {
	if rule.Backend == "" || !canaryAssigned(req, rule) {
		return candidates
	}
	if reason, disabled := g.canaries.Disabled(candidates.RouteName); disabled {
		candidates.Canary = CanaryDecision{
			Backend:        rule.Backend,
			RollbackReason: reason,
		}
		return candidates
	}
	if !containsBackend(canaryCandidates, rule.Backend) {
		g.canaries.Disable(candidates.RouteName, "backend_unavailable")
		candidates.Canary = CanaryDecision{
			Backend:        rule.Backend,
			RollbackReason: "backend_unavailable",
		}
		return candidates
	}

	if rule.Shadow {
		candidates.Canary = CanaryDecision{
			Mode:    CanaryModeShadow,
			Backend: rule.Backend,
		}
		return candidates
	}

	stable := append([]core.BackendID(nil), candidates.Fallbacks...)
	stable = appendMissingBackends(stable, candidates.IDs...)
	candidates.IDs = []core.BackendID{rule.Backend}
	candidates.Fallbacks = stable
	candidates.Canary = CanaryDecision{
		Mode:    CanaryModeLive,
		Backend: rule.Backend,
	}
	return candidates
}

func (g *Gateway) callRouted(ctx context.Context, req core.Request, candidates candidateSet) (core.Response, error) {
	if len(candidates.Fallbacks) > 0 {
		return g.callWithFallbacks(ctx, req, candidates)
	}

	remaining := copyCandidates(candidates.IDs)
	attempts := []core.BackendID{}
	for len(remaining) > 0 {
		choice := g.router.Pick(ctx, req, remaining)
		if choice == "" {
			return core.Response{}, ErrNoCandidates
		}
		g.observer.RecordRoute(ctx, "data-plane", g.router.Name(), req.ID, choice, len(remaining))
		attempts = append(attempts, choice)
		resp, err := g.callBackend(ctx, req, choice, candidates.RouteName, candidates.Canary)
		resp = annotateResponse(resp, attempts, 0, candidates.RouteName)
		if !errors.Is(err, ErrLoadShed) {
			return resp, err
		}
		remaining = without(remaining, choice)
	}
	return core.Response{}, newAttemptError(ErrLoadShed, attempts, 0)
}

func (g *Gateway) callWithFallbacks(ctx context.Context, req core.Request, candidates candidateSet) (core.Response, error) {
	chain := g.fallbackAttemptChain(ctx, req, candidates)
	if len(chain) == 0 {
		return core.Response{}, ErrNoCandidates
	}

	attempts := []core.BackendID{}
	var lastResp core.Response
	var lastErr error
	spent := 0.0

	for _, id := range chain {
		if len(attempts) > 0 && costBudgetSpent(req, spent) {
			fallbackCount := fallbackCountForAttempts(attempts)
			return annotateResponse(lastResp, attempts, fallbackCount, candidates.RouteName),
				newAttemptError(ErrFallbackBudgetExceeded, attempts, fallbackCount)
		}
		g.observer.RecordRoute(ctx, "data-plane", g.router.Name(), req.ID, id, len(chain)-len(attempts))
		attempts = append(attempts, id)
		resp, err := g.callBackend(ctx, req, id, candidates.RouteName, candidates.Canary)
		spent += resp.CostUSD
		fallbackCount := fallbackCountForAttempts(attempts)
		resp = annotateResponse(resp, attempts, fallbackCount, candidates.RouteName)
		if err == nil && !resp.Errored {
			return resp, nil
		}
		lastResp = resp
		lastErr = err
		if !retryableFailure(ctx, resp, err) {
			return resp, err
		}
	}

	fallbackCount := fallbackCountForAttempts(attempts)
	return annotateResponse(lastResp, attempts, fallbackCount, candidates.RouteName),
		newAttemptError(lastErr, attempts, fallbackCount)
}

func (g *Gateway) streamWithFallbacks(ctx context.Context, req core.Request, candidates candidateSet) (core.Stream, error) {
	chain := g.fallbackAttemptChain(ctx, req, candidates)
	if len(chain) == 0 {
		return nil, ErrNoCandidates
	}

	attempts := []core.BackendID{}
	var lastErr error
	for _, id := range chain {
		g.observer.RecordRoute(ctx, "data-plane-stream", g.router.Name(), req.ID, id, len(chain)-len(attempts))
		attempts = append(attempts, id)
		fallbackCount := fallbackCountForAttempts(attempts)
		stream, err := g.streamBackend(ctx, req, id, candidates.RouteName, candidates.Canary, attempts, fallbackCount)
		if err == nil {
			return stream, nil
		}
		lastErr = err
		if !retryableFailure(ctx, core.Response{Backend: id, Outcome: core.Outcome{Errored: true}}, err) {
			return nil, err
		}
	}

	return nil, newAttemptError(lastErr, attempts, fallbackCountForAttempts(attempts))
}

func (g *Gateway) fallbackAttemptChain(ctx context.Context, req core.Request, candidates candidateSet) []core.BackendID {
	chain := []core.BackendID{}
	if len(candidates.IDs) > 0 {
		choice := g.router.Pick(ctx, req, candidates.IDs)
		if choice != "" {
			chain = append(chain, choice)
		}
	}
	for _, id := range candidates.Fallbacks {
		if !containsBackend(chain, id) {
			chain = append(chain, id)
		}
	}
	return chain
}

func (g *Gateway) callHedged(ctx context.Context, req core.Request, candidates candidateSet) (core.Response, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	primary := g.router.Pick(ctx, req, candidates.IDs)
	if primary == "" {
		return core.Response{}, ErrNoCandidates
	}
	g.observer.RecordRoute(ctx, "data-plane", g.router.Name(), req.ID, primary, len(candidates.IDs))
	results := make(chan callResult, len(candidates.IDs))
	started := 1
	completed := 0
	extraStarted := 0
	called := map[core.BackendID]bool{primary: true}
	var first callResult

	g.startCall(runCtx, req, primary, candidates.RouteName, candidates.Canary, results, nil)
	timer := g.clock.After(g.hedgeDelay(req, primary))

	for completed < started {
		select {
		case result := <-results:
			completed++
			if first.err == nil && first.resp.Backend == "" {
				first = result
			}
			if result.ok() {
				cancel()
				return result.resp, nil
			}
			if g.canStartExtra(extraStarted, candidates.IDs) {
				if g.startHedge(runCtx, req, called, candidates, results) {
					started++
					extraStarted++
				}
			}
		case <-timer:
			if g.canStartExtra(extraStarted, candidates.IDs) {
				if g.startHedge(runCtx, req, called, candidates, results) {
					started++
					extraStarted++
				}
			}
			if g.canStartExtra(extraStarted, candidates.IDs) {
				timer = g.clock.After(g.hedgeDelay(req, primary))
			} else {
				timer = nil
			}
		case <-ctx.Done():
			return core.Response{}, ctx.Err()
		}
	}

	if first.err != nil {
		return first.resp, first.err
	}
	if first.resp.Errored {
		return first.resp, nil
	}
	return core.Response{}, ErrLoadShed
}

func (g *Gateway) shouldHedge(req core.Request, candidates []core.BackendID) bool {
	if !g.hedge.Enabled || len(candidates) <= 1 {
		return false
	}
	if g.hedge.MaxExtraCalls <= 0 {
		return false
	}
	budget := g.hedgeBudgetFraction()
	if budget <= 0 {
		return false
	}
	if budget >= 1 {
		return true
	}
	return stableFraction(req.ID) < budget
}

func (g *Gateway) hedgeBudgetFraction() float64 {
	if g.hedge.BudgetFraction == nil {
		return 1
	}
	return clamp(*g.hedge.BudgetFraction, 0, 1)
}

func (g *Gateway) hedgeDelay(req core.Request, primary core.BackendID) time.Duration {
	delay := g.hedge.Delay
	if observed, ok := g.hedgeLatencies.Percentile(primary, g.hedge.TriggerPercentile); ok {
		delay = observed
	}

	if req.Features.LatencyBudgetMs <= 0 {
		return delay
	}

	latencyBudget := time.Duration(req.Features.LatencyBudgetMs) * time.Millisecond
	if delay >= latencyBudget {
		return latencyBudget
	}
	return delay
}

func (g *Gateway) canStartExtra(extraStarted int, candidates []core.BackendID) bool {
	if extraStarted >= g.hedge.MaxExtraCalls {
		return false
	}
	return extraStarted+1 < len(candidates)
}

type callResult struct {
	resp core.Response
	err  error
}

func (r callResult) ok() bool {
	return r.err == nil && !r.resp.Errored
}

func (g *Gateway) startCall(ctx context.Context, req core.Request, id core.BackendID, routeName string, canary CanaryDecision, results chan<- callResult, done Release) {
	go func() {
		if done != nil {
			defer done()
		}
		resp, err := g.callBackend(ctx, req, id, routeName, canary)
		results <- callResult{resp: resp, err: err}
	}()
}

func (g *Gateway) startHedge(ctx context.Context, req core.Request, called map[core.BackendID]bool, candidates candidateSet, results chan<- callResult) bool {
	backup, ok := nextBackup(called, candidates.IDs)
	if !ok {
		return false
	}
	release, ok := g.acquireHedge()
	if !ok {
		return false
	}
	called[backup] = true
	g.observer.RecordRoute(ctx, "data-plane-hedge", g.router.Name(), req.ID, backup, len(candidates.IDs))
	g.startCall(ctx, req, backup, candidates.RouteName, candidates.Canary, results, release)
	return true
}

func (g *Gateway) startShadow(ctx context.Context, req core.Request, candidates candidateSet) {
	if candidates.Canary.Mode != CanaryModeShadow || candidates.Canary.Backend == "" {
		return
	}
	go func() {
		resp, err := g.callBackend(ctx, req, candidates.Canary.Backend, candidates.RouteName, candidates.Canary)
		if err != nil || resp.Errored {
			return
		}
	}()
}

func (g *Gateway) acquireHedge() (Release, bool) {
	if g.hedge.MaxInFlight <= 0 {
		return func() {}, true
	}
	for {
		current := g.hedgesInFlight.Load()
		if current >= g.hedge.MaxInFlight {
			return nil, false
		}
		if g.hedgesInFlight.CompareAndSwap(current, current+1) {
			return func() {
				g.hedgesInFlight.Add(-1)
			}, true
		}
	}
}

func (g *Gateway) callBackend(ctx context.Context, req core.Request, id core.BackendID, routeName string, canary CanaryDecision) (core.Response, error) {
	ctx, span := g.observer.Start(ctx, "backend.call",
		attribute.String("request.id", req.ID),
		attribute.String("backend.id", string(id)),
	)
	defer span.End()

	release, ok := g.acquire(req, id)
	if !ok {
		resp := core.Response{RequestID: req.ID, TenantID: req.TenantID, RouteName: routeName, Backend: id}
		resp = annotateCanaryResponse(resp, canary)
		g.status.Observe(id, resp, ErrLoadShed)
		g.observer.RecordResponse(ctx, resp, ErrLoadShed)
		g.canaries.Observe(resp, ErrLoadShed)
		return resp, ErrLoadShed
	}
	defer release()

	b := g.backends[id]
	if b == nil {
		resp := core.Response{RequestID: req.ID, TenantID: req.TenantID, RouteName: routeName, Backend: id}
		resp = annotateCanaryResponse(resp, canary)
		g.status.Observe(id, resp, ErrMissing)
		g.observer.RecordResponse(ctx, resp, ErrMissing)
		g.canaries.Observe(resp, ErrMissing)
		return resp, ErrMissing
	}

	callCtx, cancel, hasTimeout := g.backendContext(ctx, id)
	resp, err := b.Call(callCtx, req)
	cancel()
	if hasTimeout && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		err = ErrBackendTimeout
	}
	if resp.RequestID == "" {
		resp.RequestID = req.ID
	}
	if resp.TenantID == "" {
		resp.TenantID = req.TenantID
	}
	if resp.RouteName == "" {
		resp.RouteName = routeName
	}
	if resp.Backend == "" {
		resp.Backend = id
	}
	if estimate, ok := g.estimateMaxCostUSD(req, id); ok {
		resp.EstimatedCostUSD = estimate
	}
	resp = annotateCanaryResponse(resp, canary)
	g.status.Observe(id, resp, err)
	if shouldObserve(err) {
		if err == nil {
			g.router.Observe(ctx, id, resp)
		}
		for _, filter := range g.filters {
			filter.Observe(id, resp, err)
		}
	}
	if err == nil && !resp.Errored && resp.LatencyMs > 0 {
		g.hedgeLatencies.Observe(id, resp.LatencyMs)
	}
	g.observer.RecordResponse(ctx, resp, err)
	g.canaries.Observe(resp, err)
	return resp, err
}

func (g *Gateway) streamBackend(ctx context.Context, req core.Request, id core.BackendID, routeName string, canary CanaryDecision, attempts []core.BackendID, fallbackCount int) (core.Stream, error) {
	release, ok := g.acquire(req, id)
	if !ok {
		resp := core.Response{RequestID: req.ID, TenantID: req.TenantID, RouteName: routeName, Backend: id}
		resp = annotateCanaryResponse(resp, canary)
		g.status.Observe(id, resp, ErrLoadShed)
		g.observer.RecordResponse(ctx, resp, ErrLoadShed)
		g.canaries.Observe(resp, ErrLoadShed)
		return nil, ErrLoadShed
	}

	b := g.backends[id]
	if b == nil {
		release()
		resp := core.Response{RequestID: req.ID, TenantID: req.TenantID, RouteName: routeName, Backend: id}
		resp = annotateCanaryResponse(resp, canary)
		g.status.Observe(id, resp, ErrMissing)
		g.observer.RecordResponse(ctx, resp, ErrMissing)
		g.canaries.Observe(resp, ErrMissing)
		return nil, ErrMissing
	}
	streamBackend, ok := b.(backend.StreamBackend)
	if !ok {
		release()
		resp := core.Response{RequestID: req.ID, TenantID: req.TenantID, RouteName: routeName, Backend: id}
		resp = annotateCanaryResponse(resp, canary)
		g.status.Observe(id, resp, ErrStreaming)
		g.observer.RecordResponse(ctx, resp, ErrStreaming)
		g.canaries.Observe(resp, ErrStreaming)
		return nil, ErrStreaming
	}

	streamCtx, cancel, hasTimeout := g.backendContext(ctx, id)
	stream, err := streamBackend.Stream(streamCtx, req)
	if err != nil {
		cancel()
		release()
		if hasTimeout && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = ErrBackendTimeout
		}
		resp := core.Response{RequestID: req.ID, TenantID: req.TenantID, RouteName: routeName, Backend: id, Outcome: core.Outcome{Errored: true}}
		resp = annotateCanaryResponse(resp, canary)
		g.observeStreamResponse(ctx, id, resp, err)
		return nil, err
	}
	estimate, _ := g.estimateMaxCostUSD(req, id)
	return &gatewayStream{
		ctx:              ctx,
		gateway:          g,
		req:              req,
		id:               id,
		routeName:        routeName,
		canary:           canary,
		attempts:         append([]core.BackendID(nil), attempts...),
		fallbackCount:    fallbackCount,
		estimatedCostUSD: estimate,
		stream:           stream,
		release:          release,
		cancel:           cancel,
		hasTimeout:       hasTimeout,
	}, nil
}

func (g *Gateway) observeStreamResponse(ctx context.Context, id core.BackendID, resp core.Response, err error) {
	g.status.Observe(id, resp, err)
	if shouldObserve(err) {
		if err == nil {
			g.router.Observe(ctx, id, resp)
		}
		for _, filter := range g.filters {
			filter.Observe(id, resp, err)
		}
	}
	g.observer.RecordResponse(ctx, resp, err)
	g.canaries.Observe(resp, err)
}

type gatewayStream struct {
	ctx              context.Context
	gateway          *Gateway
	req              core.Request
	id               core.BackendID
	routeName        string
	canary           CanaryDecision
	attempts         []core.BackendID
	fallbackCount    int
	estimatedCostUSD float64
	stream           core.Stream
	release          Release
	cancel           context.CancelFunc
	hasTimeout       bool
	observed         bool
	closed           bool
}

func (s *gatewayStream) Recv() (core.StreamChunk, error) {
	chunk, err := s.stream.Recv()
	if chunk.RequestID == "" {
		chunk.RequestID = s.req.ID
	}
	if chunk.TenantID == "" {
		chunk.TenantID = s.req.TenantID
	}
	if chunk.RouteName == "" {
		chunk.RouteName = s.routeName
	}
	if chunk.Backend == "" {
		chunk.Backend = s.id
	}
	chunk = annotateCanaryChunk(chunk, s.canary)
	chunk = annotateStreamChunk(chunk, s.attempts, s.fallbackCount)
	if err != nil {
		if s.hasTimeout && errors.Is(err, context.DeadlineExceeded) && s.ctx.Err() == nil {
			err = ErrBackendTimeout
		}
		if !errors.Is(err, io.EOF) {
			resp := core.Response{RequestID: s.req.ID, TenantID: s.req.TenantID, RouteName: s.routeName, Backend: s.id, Outcome: core.Outcome{Errored: true}}
			s.observe(resp, err)
		}
		s.closeRelease()
		return chunk, err
	}
	if chunk.Done {
		resp := core.Response{
			RequestID:  s.req.ID,
			TenantID:   s.req.TenantID,
			RouteName:  s.routeName,
			Backend:    s.id,
			OutputText: "",
			Outcome:    chunk.Outcome,
		}
		resp = annotateCanaryResponse(resp, s.canary)
		s.observe(resp, nil)
		s.closeRelease()
	}
	return chunk, nil
}

func (s *gatewayStream) Close() error {
	err := s.stream.Close()
	s.closeRelease()
	return err
}

func (s *gatewayStream) BackendID() core.BackendID {
	return s.id
}

func (s *gatewayStream) RouteName() string {
	return s.routeName
}

func (s *gatewayStream) AttemptedBackends() []core.BackendID {
	return append([]core.BackendID(nil), s.attempts...)
}

func (s *gatewayStream) FallbackCount() int {
	return s.fallbackCount
}

func (s *gatewayStream) EstimatedCostUSD() float64 {
	return s.estimatedCostUSD
}

func (s *gatewayStream) CanaryMode() string {
	return s.canary.Mode
}

func (s *gatewayStream) CanaryBackend() core.BackendID {
	return s.canary.Backend
}

func (s *gatewayStream) CanaryRollback() string {
	return s.canary.RollbackReason
}

func (s *gatewayStream) observe(resp core.Response, err error) {
	if s.observed {
		return
	}
	s.observed = true
	s.gateway.observeStreamResponse(s.ctx, s.id, resp, err)
}

func (s *gatewayStream) closeRelease() {
	if s.closed {
		return
	}
	s.closed = true
	s.release()
	if s.cancel != nil {
		s.cancel()
	}
}

func (g *Gateway) acquire(req core.Request, id core.BackendID) (Release, bool) {
	releases := make([]Release, 0, len(g.filters))
	for _, filter := range g.filters {
		release, ok := filter.Acquire(req, id)
		if !ok {
			releaseAll(releases)
			return nil, false
		}
		releases = append(releases, release)
	}
	return func() {
		releaseAll(releases)
	}, true
}

func (g *Gateway) backendContext(ctx context.Context, id core.BackendID) (context.Context, context.CancelFunc, bool) {
	timeout := g.timeouts[id]
	if timeout <= 0 {
		return ctx, func() {}, false
	}
	next, cancel := context.WithTimeout(ctx, timeout)
	return next, cancel, true
}

func releaseAll(releases []Release) {
	for i := len(releases) - 1; i >= 0; i-- {
		releases[i]()
	}
}

func shouldObserve(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func (g *Gateway) recordGatewayError(ctx context.Context, resp core.Response, err error) {
	if err != nil && resp.Backend == "" {
		g.observer.RecordResponse(ctx, resp, err)
	}
}

func without(ids []core.BackendID, drop core.BackendID) []core.BackendID {
	out := make([]core.BackendID, 0, len(ids)-1)
	for _, id := range ids {
		if id != drop {
			out = append(out, id)
		}
	}
	return out
}

func nextBackup(called map[core.BackendID]bool, candidates []core.BackendID) (core.BackendID, bool) {
	for _, id := range candidates {
		if !called[id] {
			return id, true
		}
	}
	return "", false
}

func stableFraction(value string) float64 {
	hash := fnv.New64a()
	hash.Write([]byte(value))
	return float64(hash.Sum64()) / float64(^uint64(0))
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
