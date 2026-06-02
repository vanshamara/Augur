package dataplane

import (
	"context"
	"sync"

	"github.com/vanshamara/Augur/internal/core"
)

type KeyFunc func(core.Request) string

type SingleFlight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	done chan struct{}
	resp core.Response
	err  error
}

func NewSingleFlight() *SingleFlight {
	return &SingleFlight{calls: map[string]*flightCall{}}
}

// Do shares one in-flight call across matching keys.
func (s *SingleFlight) Do(ctx context.Context, key string, fn func() (core.Response, error)) (core.Response, error) {
	if key == "" {
		return fn()
	}

	s.mu.Lock()
	if call := s.calls[key]; call != nil {
		s.mu.Unlock()
		return waitForFlight(ctx, call)
	}

	call := &flightCall{done: make(chan struct{})}
	s.calls[key] = call
	s.mu.Unlock()

	call.resp, call.err = fn()
	close(call.done)

	s.mu.Lock()
	delete(s.calls, key)
	s.mu.Unlock()

	return call.resp, call.err
}

func waitForFlight(ctx context.Context, call *flightCall) (core.Response, error) {
	select {
	case <-call.done:
		return call.resp, call.err
	case <-ctx.Done():
		return core.Response{}, ctx.Err()
	}
}
