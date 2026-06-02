package mock

import (
	"context"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

// Backend is a simulated model endpoint. Its outcomes are a fixed function of the
// request id, the backend id, the seed, and the current time. That means a run can
// be replayed exactly, and any backend can be asked what it would have done for a
// request it never actually handled, which is what the oracle and shadow traffic need.
type Backend struct {
	id      core.BackendID
	profile Profile
	start   time.Time
	deriver *rng.Deriver
	clk     clock.Clock
}

// New builds a mock backend. start marks elapsed time zero for the profile drift.
func New(id core.BackendID, profile Profile, start time.Time, deriver *rng.Deriver, clk clock.Clock) *Backend {
	return &Backend{id: id, profile: profile, start: start, deriver: deriver, clk: clk}
}

func (b *Backend) ID() core.BackendID {
	return b.id
}

// TrueParams returns the backend's true parameters at the given time. The oracle
// uses this to know the best possible choice without having to sample.
func (b *Backend) TrueParams(at time.Time) Params {
	return b.profile.ParamsAt(at.Sub(b.start))
}

// CostPerToken is the published price the gateway can see ahead of time. It does not
// change over time in any profile, so the cost aware router can rely on it.
func (b *Backend) CostPerToken() float64 {
	return b.profile.ParamsAt(0).CostPerToken
}

// Outcome returns the result this backend would produce for the request at the
// given time. The same inputs always give the same outcome, so two callers asking
// the same question get the same answer.
func (b *Backend) Outcome(req core.Request, at time.Time) core.Outcome {
	params := b.TrueParams(at)
	generator := b.deriver.Rand(rng.HashKey(string(b.id)), rng.HashKey(req.ID))

	latencyFactor := 1 + params.LatencySpread*(2*generator.Float64()-1)
	latency := params.MeanLatencyMs * latencyFactor
	if latency < 1 {
		latency = 1
	}

	errored := generator.Float64() < params.ErrorRate
	outputTokens := 50 + generator.IntN(450)
	cost := float64(req.Features.PromptTokens+outputTokens) * params.CostPerToken

	return core.Outcome{
		LatencyMs:    latency,
		CostUSD:      cost,
		OutputTokens: outputTokens,
		Errored:      errored,
	}
}

// Call runs the request at the current time and returns the result. The harness
// applies the latency in virtual time, so this does not block.
func (b *Backend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	outcome := b.Outcome(req, b.clk.Now())
	return core.Response{RequestID: req.ID, Backend: b.id, Outcome: outcome}, nil
}
