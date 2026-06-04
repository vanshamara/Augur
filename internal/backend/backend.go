package backend

import (
	"context"

	"github.com/vanshamara/Augur/internal/core"
)

// Backend is one model endpoint the gateway can route a request to.
type Backend interface {
	ID() core.BackendID
	Call(ctx context.Context, req core.Request) (core.Response, error)
}

type HealthChecker interface {
	Check(ctx context.Context) error
}

type StreamBackend interface {
	Backend
	Stream(ctx context.Context, req core.Request) (core.Stream, error)
}
