package router

import (
	"context"

	"github.com/vanshamara/Augur/internal/core"
)

// Router decides which backend handles a request. Pick chooses from the candidates.
// Observe lets a router update its own signals once a request finishes, which is how
// the load and latency aware routers learn what is going on.
type Router interface {
	Name() string
	Pick(ctx context.Context, req core.Request, candidates []core.BackendID) core.BackendID
	Observe(ctx context.Context, choice core.BackendID, resp core.Response)
}

// Static always sends requests to one chosen backend, or the first candidate if
// that backend is not on offer.
type Static struct {
	target core.BackendID
}

func NewStatic(target core.BackendID) *Static {
	return &Static{target: target}
}

func (s *Static) Name() string {
	return "static"
}

func (s *Static) Pick(ctx context.Context, req core.Request, candidates []core.BackendID) core.BackendID {
	for _, id := range candidates {
		if id == s.target {
			return id
		}
	}
	return candidates[0]
}

func (s *Static) Observe(ctx context.Context, choice core.BackendID, resp core.Response) {
}
