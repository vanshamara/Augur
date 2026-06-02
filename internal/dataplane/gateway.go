package dataplane

import (
	"context"
	"errors"
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
	ErrNoCandidates = errors.New("no backend candidates")
	ErrLoadShed     = errors.New("request shed by data plane")
	ErrMissing      = errors.New("backend is not registered")
	ErrStreaming    = errors.New("backend does not support streaming")
)

type HedgeConfig struct {
	Enabled     bool
	Delay       time.Duration
	MaxInFlight int64
}

type Config struct {
	Router          router.Router
	Backends        []backend.Backend
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
	filters         []Filter
	clock           clock.Clock
	hedge           HedgeConfig
	hedgesInFlight  atomic.Int64
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

	return &Gateway{
		router:          config.Router,
		ids:             ids,
		backends:        backends,
		filters:         append([]Filter(nil), config.Filters...),
		clock:           config.Clock,
		hedge:           config.Hedge,
		singleFlight:    config.SingleFlight,
		singleFlightKey: config.SingleFlightKey,
		observer:        config.Observer,
	}, nil
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
	if len(candidates) == 0 {
		return nil, ErrNoCandidates
	}

	remaining := copyCandidates(candidates)
	for len(remaining) > 0 {
		choice := g.router.Pick(ctx, req, remaining)
		if choice == "" {
			return nil, ErrNoCandidates
		}
		g.observer.RecordRoute(ctx, "data-plane-stream", g.router.Name(), req.ID, choice, len(remaining))
		stream, err := g.streamBackend(ctx, req, choice)
		if !errors.Is(err, ErrLoadShed) {
			return stream, err
		}
		remaining = without(remaining, choice)
	}
	return nil, ErrLoadShed
}

func (g *Gateway) callUnique(ctx context.Context, req core.Request) (core.Response, error) {
	candidates := g.candidates(req)
	if len(candidates) == 0 {
		return core.Response{}, ErrNoCandidates
	}
	if !g.hedge.Enabled || len(candidates) == 1 {
		return g.callRouted(ctx, req, candidates)
	}
	return g.callHedged(ctx, req, candidates)
}

func (g *Gateway) candidates(req core.Request) []core.BackendID {
	candidates := copyCandidates(g.ids)
	for _, filter := range g.filters {
		candidates = filter.Apply(req, candidates)
		if len(candidates) == 0 {
			return nil
		}
	}
	return candidates
}

func (g *Gateway) callRouted(ctx context.Context, req core.Request, candidates []core.BackendID) (core.Response, error) {
	remaining := copyCandidates(candidates)
	for len(remaining) > 0 {
		choice := g.router.Pick(ctx, req, remaining)
		if choice == "" {
			return core.Response{}, ErrNoCandidates
		}
		g.observer.RecordRoute(ctx, "data-plane", g.router.Name(), req.ID, choice, len(remaining))
		resp, err := g.callBackend(ctx, req, choice)
		if !errors.Is(err, ErrLoadShed) {
			return resp, err
		}
		remaining = without(remaining, choice)
	}
	return core.Response{}, ErrLoadShed
}

func (g *Gateway) callHedged(ctx context.Context, req core.Request, candidates []core.BackendID) (core.Response, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	primary := g.router.Pick(ctx, req, candidates)
	if primary == "" {
		return core.Response{}, ErrNoCandidates
	}
	g.observer.RecordRoute(ctx, "data-plane", g.router.Name(), req.ID, primary, len(candidates))
	results := make(chan callResult, 2)
	started := 1
	completed := 0
	hedged := false
	var first callResult

	g.startCall(runCtx, req, primary, results, nil)
	timer := g.clock.After(g.hedge.Delay)

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
			if !hedged {
				if g.startHedge(runCtx, req, primary, candidates, results) {
					started++
					hedged = true
				}
			}
		case <-timer:
			if !hedged {
				if g.startHedge(runCtx, req, primary, candidates, results) {
					started++
					hedged = true
				}
			}
			timer = nil
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

type callResult struct {
	resp core.Response
	err  error
}

func (r callResult) ok() bool {
	return r.err == nil && !r.resp.Errored
}

func (g *Gateway) startCall(ctx context.Context, req core.Request, id core.BackendID, results chan<- callResult, done Release) {
	go func() {
		if done != nil {
			defer done()
		}
		resp, err := g.callBackend(ctx, req, id)
		results <- callResult{resp: resp, err: err}
	}()
}

func (g *Gateway) startHedge(ctx context.Context, req core.Request, primary core.BackendID, candidates []core.BackendID, results chan<- callResult) bool {
	backup, ok := firstBackup(primary, candidates)
	if !ok {
		return false
	}
	release, ok := g.acquireHedge()
	if !ok {
		return false
	}
	g.observer.RecordRoute(ctx, "data-plane-hedge", g.router.Name(), req.ID, backup, len(candidates))
	g.startCall(ctx, req, backup, results, release)
	return true
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

func (g *Gateway) callBackend(ctx context.Context, req core.Request, id core.BackendID) (core.Response, error) {
	ctx, span := g.observer.Start(ctx, "backend.call",
		attribute.String("request.id", req.ID),
		attribute.String("backend.id", string(id)),
	)
	defer span.End()

	release, ok := g.acquire(id)
	if !ok {
		resp := core.Response{RequestID: req.ID, Backend: id}
		g.observer.RecordResponse(ctx, resp, ErrLoadShed)
		return resp, ErrLoadShed
	}
	defer release()

	b := g.backends[id]
	if b == nil {
		resp := core.Response{RequestID: req.ID, Backend: id}
		g.observer.RecordResponse(ctx, resp, ErrMissing)
		return resp, ErrMissing
	}

	resp, err := b.Call(ctx, req)
	if resp.RequestID == "" {
		resp.RequestID = req.ID
	}
	if resp.Backend == "" {
		resp.Backend = id
	}
	if shouldObserve(err) {
		if err == nil {
			g.router.Observe(ctx, id, resp)
		}
		for _, filter := range g.filters {
			filter.Observe(id, resp, err)
		}
	}
	g.observer.RecordResponse(ctx, resp, err)
	return resp, err
}

func (g *Gateway) streamBackend(ctx context.Context, req core.Request, id core.BackendID) (core.Stream, error) {
	release, ok := g.acquire(id)
	if !ok {
		resp := core.Response{RequestID: req.ID, Backend: id}
		g.observer.RecordResponse(ctx, resp, ErrLoadShed)
		return nil, ErrLoadShed
	}

	b := g.backends[id]
	if b == nil {
		release()
		resp := core.Response{RequestID: req.ID, Backend: id}
		g.observer.RecordResponse(ctx, resp, ErrMissing)
		return nil, ErrMissing
	}
	streamBackend, ok := b.(backend.StreamBackend)
	if !ok {
		release()
		resp := core.Response{RequestID: req.ID, Backend: id}
		g.observer.RecordResponse(ctx, resp, ErrStreaming)
		return nil, ErrStreaming
	}

	stream, err := streamBackend.Stream(ctx, req)
	if err != nil {
		release()
		resp := core.Response{RequestID: req.ID, Backend: id, Outcome: core.Outcome{Errored: true}}
		g.observeStreamResponse(ctx, id, resp, err)
		return nil, err
	}
	return &gatewayStream{
		ctx:     ctx,
		gateway: g,
		req:     req,
		id:      id,
		stream:  stream,
		release: release,
	}, nil
}

func (g *Gateway) observeStreamResponse(ctx context.Context, id core.BackendID, resp core.Response, err error) {
	if shouldObserve(err) {
		if err == nil {
			g.router.Observe(ctx, id, resp)
		}
		for _, filter := range g.filters {
			filter.Observe(id, resp, err)
		}
	}
	g.observer.RecordResponse(ctx, resp, err)
}

type gatewayStream struct {
	ctx      context.Context
	gateway  *Gateway
	req      core.Request
	id       core.BackendID
	stream   core.Stream
	release  Release
	observed bool
	closed   bool
}

func (s *gatewayStream) Recv() (core.StreamChunk, error) {
	chunk, err := s.stream.Recv()
	if chunk.RequestID == "" {
		chunk.RequestID = s.req.ID
	}
	if chunk.Backend == "" {
		chunk.Backend = s.id
	}
	if err != nil {
		if !errors.Is(err, io.EOF) {
			resp := core.Response{RequestID: s.req.ID, Backend: s.id, Outcome: core.Outcome{Errored: true}}
			s.observe(resp, err)
		}
		s.closeRelease()
		return chunk, err
	}
	if chunk.Done {
		resp := core.Response{
			RequestID:  s.req.ID,
			Backend:    s.id,
			OutputText: "",
			Outcome:    chunk.Outcome,
		}
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
}

func (g *Gateway) acquire(id core.BackendID) (Release, bool) {
	releases := make([]Release, 0, len(g.filters))
	for _, filter := range g.filters {
		release, ok := filter.Acquire(id)
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

func firstBackup(primary core.BackendID, candidates []core.BackendID) (core.BackendID, bool) {
	for _, id := range candidates {
		if id != primary {
			return id, true
		}
	}
	return "", false
}
