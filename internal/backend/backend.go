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
