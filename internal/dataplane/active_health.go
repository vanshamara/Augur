package dataplane

import (
	"context"
	"time"

	"github.com/vanshamara/Augur/internal/backend"
)

const (
	defaultHealthInterval = 5 * time.Second
	defaultHealthTimeout  = 2 * time.Second
)

type ActiveHealthConfig struct {
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold int
	SuccessThreshold int
}

type ActiveHealth struct {
	config   ActiveHealthConfig
	filter   *HealthFilter
	backends []backend.Backend
	failures map[string]int
	success  map[string]int
}

func NewActiveHealth(config ActiveHealthConfig, filter *HealthFilter, backends []backend.Backend) *ActiveHealth {
	if config.Interval <= 0 {
		config.Interval = defaultHealthInterval
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultHealthTimeout
	}
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 1
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 1
	}
	return &ActiveHealth{
		config:   config,
		filter:   filter,
		backends: append([]backend.Backend(nil), backends...),
		failures: map[string]int{},
		success:  map[string]int{},
	}
}

func (a *ActiveHealth) Start(ctx context.Context) func() {
	runCtx, cancel := context.WithCancel(ctx)
	go a.run(runCtx)
	return cancel
}

func (a *ActiveHealth) CheckOnce(ctx context.Context) {
	if a == nil || a.filter == nil {
		return
	}
	for _, candidate := range a.backends {
		a.checkBackend(ctx, candidate)
	}
}

func (a *ActiveHealth) run(ctx context.Context) {
	a.CheckOnce(ctx)
	ticker := time.NewTicker(a.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.CheckOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (a *ActiveHealth) checkBackend(ctx context.Context, candidate backend.Backend) {
	id := candidate.ID()
	checkCtx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()

	err := a.check(checkCtx, candidate)
	key := string(id)
	if err != nil {
		a.failures[key]++
		a.success[key] = 0
		if a.failures[key] >= a.config.FailureThreshold {
			a.filter.RecordCheck(id, false, time.Now(), err)
		}
		a.filter.SetCounters(id, a.failures[key], a.success[key])
		return
	}

	a.success[key]++
	a.failures[key] = 0
	if a.success[key] >= a.config.SuccessThreshold {
		a.filter.RecordCheck(id, true, time.Now(), nil)
	}
	a.filter.SetCounters(id, a.failures[key], a.success[key])
}

func (a *ActiveHealth) check(ctx context.Context, candidate backend.Backend) error {
	checker, ok := candidate.(backend.HealthChecker)
	if !ok {
		return nil
	}
	return checker.Check(ctx)
}
