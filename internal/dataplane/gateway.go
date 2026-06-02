package dataplane

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

var (
	ErrNoCandidates = errors.New("no backend candidates")
	ErrLoadShed     = errors.New("request shed by data plane")
	ErrMissing      = errors.New("backend is not registered")
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
	}, nil
}

// Call routes one request through the filter chain and backend fleet.
func (g *Gateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	if g.singleFlight != nil && g.singleFlightKey != nil {
		key := g.singleFlightKey(req)
		return g.singleFlight.Do(ctx, key, func() (core.Response, error) {
			return g.callUnique(ctx, req)
		})
	}
	return g.callUnique(ctx, req)
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
		choice := g.router.Pick(req, remaining)
		if choice == "" {
			return core.Response{}, ErrNoCandidates
		}
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

	primary := g.router.Pick(req, candidates)
	if primary == "" {
		return core.Response{}, ErrNoCandidates
	}
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
	release, ok := g.acquire(id)
	if !ok {
		return core.Response{}, ErrLoadShed
	}
	defer release()

	b := g.backends[id]
	if b == nil {
		return core.Response{}, ErrMissing
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
			g.router.Observe(id, resp)
		}
		for _, filter := range g.filters {
			filter.Observe(id, resp, err)
		}
	}
	return resp, err
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
