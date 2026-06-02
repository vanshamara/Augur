package dataplane

import (
	"sync"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
)

type CircuitConfig struct {
	FailureThreshold int
	RecoveryAfter    time.Duration
	HalfOpenMax      int
	Clock            clock.Clock
}

type circuitMode int

const (
	circuitClosed circuitMode = iota
	circuitOpen
	circuitHalfOpen
)

type circuitState struct {
	mode     circuitMode
	failures int
	openedAt time.Time
	probes   int
}

type CircuitBreaker struct {
	mu               sync.Mutex
	states           map[core.BackendID]*circuitState
	failureThreshold int
	recoveryAfter    time.Duration
	halfOpenMax      int
	clock            clock.Clock
}

func NewCircuitBreaker(ids []core.BackendID, config CircuitConfig) *CircuitBreaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 1
	}
	if config.RecoveryAfter <= 0 {
		config.RecoveryAfter = time.Second
	}
	if config.HalfOpenMax <= 0 {
		config.HalfOpenMax = 1
	}
	if config.Clock == nil {
		config.Clock = clock.NewReal()
	}

	states := make(map[core.BackendID]*circuitState, len(ids))
	for _, id := range ids {
		states[id] = &circuitState{mode: circuitClosed}
	}

	return &CircuitBreaker{
		states:           states,
		failureThreshold: config.FailureThreshold,
		recoveryAfter:    config.RecoveryAfter,
		halfOpenMax:      config.HalfOpenMax,
		clock:            config.Clock,
	}
}

func (c *CircuitBreaker) Name() string {
	return "circuit"
}

func (c *CircuitBreaker) Apply(req core.Request, candidates []core.BackendID) []core.BackendID {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]core.BackendID, 0, len(candidates))
	now := c.clock.Now()
	for _, id := range candidates {
		state := c.states[id]
		if state.mode == circuitOpen && now.Sub(state.openedAt) >= c.recoveryAfter {
			state.mode = circuitHalfOpen
		}
		if state.mode != circuitOpen {
			out = append(out, id)
		}
	}
	return out
}

func (c *CircuitBreaker) Acquire(id core.BackendID) (Release, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.states[id]
	now := c.clock.Now()
	if state.mode == circuitOpen && now.Sub(state.openedAt) >= c.recoveryAfter {
		state.mode = circuitHalfOpen
	}
	if state.mode == circuitOpen {
		return nil, false
	}
	if state.mode != circuitHalfOpen {
		return func() {}, true
	}
	if state.probes >= c.halfOpenMax {
		return nil, false
	}

	state.probes++
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if state.probes > 0 {
			state.probes--
		}
	}, true
}

func (c *CircuitBreaker) Observe(id core.BackendID, resp core.Response, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.states[id]
	failed := err != nil || resp.Errored
	if failed {
		c.open(state)
		return
	}

	if state.mode == circuitHalfOpen {
		c.close(state)
		return
	}

	if state.mode == circuitClosed {
		state.failures = 0
	}
}

func (c *CircuitBreaker) Mode(id core.BackendID) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.states[id].mode {
	case circuitOpen:
		return "open"
	case circuitHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

func (c *CircuitBreaker) open(state *circuitState) {
	if state.mode == circuitClosed {
		state.failures++
		if state.failures < c.failureThreshold {
			return
		}
	}
	state.mode = circuitOpen
	state.failures = 0
	state.openedAt = c.clock.Now()
}

func (c *CircuitBreaker) close(state *circuitState) {
	state.mode = circuitClosed
	state.failures = 0
	state.probes = 0
}
