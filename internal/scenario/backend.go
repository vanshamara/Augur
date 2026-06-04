package scenario

import (
	"context"
	"fmt"

	"github.com/vanshamara/Augur/internal/core"
)

// scriptBackend is a deterministic stand-in for a model backend. It returns a
// fixed latency and cost, and can be told to fail calls or health checks so a
// scenario can show how the gateway reacts.
type scriptBackend struct {
	id        core.BackendID
	latencyMs float64
	costUSD   float64
	callErr   error
	errored   bool
	checkErr  error
}

func (b *scriptBackend) ID() core.BackendID {
	return b.id
}

func (b *scriptBackend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	if b.callErr != nil || b.errored {
		return core.Response{Backend: b.id, Outcome: core.Outcome{LatencyMs: b.latencyMs, Errored: true}}, b.callErr
	}
	return core.Response{
		Backend:    b.id,
		OutputText: "ok",
		Outcome: core.Outcome{
			LatencyMs:    b.latencyMs,
			CostUSD:      b.costUSD,
			OutputTokens: 16,
		},
	}, nil
}

func (b *scriptBackend) Check(ctx context.Context) error {
	return b.checkErr
}

type statusError struct {
	code int
}

func (e statusError) Error() string {
	return fmt.Sprintf("upstream status %d", e.code)
}

func (e statusError) StatusCode() int {
	return e.code
}
