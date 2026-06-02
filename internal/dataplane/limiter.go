package dataplane

import (
	"sync/atomic"

	"github.com/vanshamara/Augur/internal/core"
)

type LimitConfig struct {
	InitialLimit    int64
	MinLimit        int64
	MaxLimit        int64
	TargetLatencyMs float64
}

type limitState struct {
	inFlight atomic.Int64
	limit    atomic.Int64
}

type AdaptiveLimiter struct {
	states          map[core.BackendID]*limitState
	minLimit        int64
	maxLimit        int64
	targetLatencyMs float64
}

func NewAdaptiveLimiter(ids []core.BackendID, config LimitConfig) *AdaptiveLimiter {
	if config.MinLimit <= 0 {
		config.MinLimit = 1
	}
	if config.InitialLimit < config.MinLimit {
		config.InitialLimit = config.MinLimit
	}
	if config.MaxLimit < config.InitialLimit {
		config.MaxLimit = config.InitialLimit
	}
	if config.TargetLatencyMs <= 0 {
		config.TargetLatencyMs = 1000
	}

	states := make(map[core.BackendID]*limitState, len(ids))
	for _, id := range ids {
		state := &limitState{}
		state.limit.Store(config.InitialLimit)
		states[id] = state
	}

	return &AdaptiveLimiter{
		states:          states,
		minLimit:        config.MinLimit,
		maxLimit:        config.MaxLimit,
		targetLatencyMs: config.TargetLatencyMs,
	}
}

func (l *AdaptiveLimiter) Name() string {
	return "concurrency"
}

func (l *AdaptiveLimiter) Apply(req core.Request, candidates []core.BackendID) []core.BackendID {
	out := make([]core.BackendID, 0, len(candidates))
	for _, id := range candidates {
		state := l.states[id]
		if state.inFlight.Load() < state.limit.Load() {
			out = append(out, id)
		}
	}
	return out
}

func (l *AdaptiveLimiter) Acquire(req core.Request, id core.BackendID) (Release, bool) {
	state := l.states[id]
	for {
		current := state.inFlight.Load()
		if current >= state.limit.Load() {
			return nil, false
		}
		if state.inFlight.CompareAndSwap(current, current+1) {
			return func() {
				state.inFlight.Add(-1)
			}, true
		}
	}
}

func (l *AdaptiveLimiter) Observe(id core.BackendID, resp core.Response, err error) {
	state := l.states[id]
	if err != nil || resp.Errored || resp.LatencyMs > l.targetLatencyMs {
		l.decrease(state)
		return
	}
	l.increase(state)
}

func (l *AdaptiveLimiter) Limit(id core.BackendID) int64 {
	return l.states[id].limit.Load()
}

func (l *AdaptiveLimiter) InFlight(id core.BackendID) int64 {
	return l.states[id].inFlight.Load()
}

func (l *AdaptiveLimiter) increase(state *limitState) {
	for {
		current := state.limit.Load()
		if current >= l.maxLimit {
			return
		}
		next := current + 1
		if next > l.maxLimit {
			next = l.maxLimit
		}
		if state.limit.CompareAndSwap(current, next) {
			return
		}
	}
}

func (l *AdaptiveLimiter) decrease(state *limitState) {
	for {
		current := state.limit.Load()
		if current <= l.minLimit {
			return
		}
		next := current / 2
		if next < l.minLimit {
			next = l.minLimit
		}
		if state.limit.CompareAndSwap(current, next) {
			return
		}
	}
}
