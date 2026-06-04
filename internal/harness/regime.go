package harness

import (
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

// Regime is a named backend setup that exercises one failure mode. Build returns a
// fresh backend set seeded by seed, so every router in a seed sees the same draws.
type Regime struct {
	Name       string
	Build      func(seed uint64, clk *clock.Virtual, start time.Time) []*mock.Backend
	BuildTrace func(seed uint64, count int, start time.Time) Trace
	Policy     func() *control.Policy
}

// StableRegime has three steady backends and nothing drifting.
func StableRegime() Regime {
	return Regime{Name: "stable", Build: func(seed uint64, clk *clock.Virtual, start time.Time) []*mock.Backend {
		d := rng.NewDeriver(seed)
		return []*mock.Backend{
			mock.New("cheap", mock.CheapLowerQuality(), start, d, clk),
			mock.New("fast", mock.FastFlaky(), start, d, clk),
			mock.New("stable", mock.SlowStable(), start, d, clk),
		}
	}}
}

// RisingP99Regime starts with the rising backend as the fastest, then it degrades,
// so a good router has to notice and move off it.
func RisingP99Regime() Regime {
	return Regime{Name: "rising-p99", Build: func(seed uint64, clk *clock.Virtual, start time.Time) []*mock.Backend {
		d := rng.NewDeriver(seed)
		return []*mock.Backend{
			mock.New("rising", mock.RisingP99(), start, d, clk),
			mock.New("cheap", mock.CheapLowerQuality(), start, d, clk),
			mock.New("stable", mock.SlowStable(), start, d, clk),
		}
	}}
}

// Intermittent500sRegime includes a backend that throws bursts of errors.
func Intermittent500sRegime() Regime {
	return Regime{Name: "intermittent-500s", Build: func(seed uint64, clk *clock.Virtual, start time.Time) []*mock.Backend {
		d := rng.NewDeriver(seed)
		return []*mock.Backend{
			mock.New("flaky-500", mock.Intermittent500s(), start, d, clk),
			mock.New("cheap", mock.CheapLowerQuality(), start, d, clk),
			mock.New("stable", mock.SlowStable(), start, d, clk),
		}
	}}
}

// ColdStartRegime includes a backend that is very slow at first and warms up.
func ColdStartRegime() Regime {
	return Regime{Name: "cold-start", Build: func(seed uint64, clk *clock.Virtual, start time.Time) []*mock.Backend {
		d := rng.NewDeriver(seed)
		return []*mock.Backend{
			mock.New("cold", mock.ColdStart(), start, d, clk),
			mock.New("fast", mock.FastFlaky(), start, d, clk),
			mock.New("stable", mock.SlowStable(), start, d, clk),
		}
	}}
}

func RequestShapeRegime() Regime {
	return Regime{
		Name:       "request-shape",
		BuildTrace: GenerateRequestShapeTrace,
		Policy:     RequestShapePolicy,
		Build: func(seed uint64, clk *clock.Virtual, start time.Time) []*mock.Backend {
			d := rng.NewDeriver(seed)
			return []*mock.Backend{
				mock.New("cheap-chat", mock.CheapChatSpecialist(), start, d, clk),
				mock.New("balanced", mock.BalancedGeneralist(), start, d, clk),
				mock.New("strong-reasoning", mock.StrongReasoningSpecialist(), start, d, clk),
			}
		},
	}
}

func RequestShapePolicy() *control.Policy {
	return control.NewPolicy(control.PolicyConfig{
		ID: "request-shape",
		Constraints: control.ConstraintConfig{
			MinQuality:   0.85,
			MaxErrorRate: 0.10,
		},
		Objective: control.ObjectiveConfig{
			Type: control.MinimizeLatency,
		},
		OnInfeasible: control.InfeasibleBestEffort,
	})
}

// AllRegimes returns the regimes the comparison runs over.
func AllRegimes() []Regime {
	return []Regime{StableRegime(), RequestShapeRegime(), RisingP99Regime(), Intermittent500sRegime(), ColdStartRegime()}
}

func traceForRegime(regime Regime, seed uint64, requests int, start time.Time) Trace {
	if regime.BuildTrace != nil {
		return regime.BuildTrace(seed, requests, start)
	}
	return GenerateTrace(seed, requests, start)
}

func policyForRegime(regime Regime) *control.Policy {
	if regime.Policy != nil {
		return regime.Policy()
	}
	return DefaultComparisonPolicy()
}

func idsOf(backends []*mock.Backend) []core.BackendID {
	ids := make([]core.BackendID, len(backends))
	for i, b := range backends {
		ids[i] = b.ID()
	}
	return ids
}

func pricesOf(backends []*mock.Backend) map[core.BackendID]float64 {
	prices := make(map[core.BackendID]float64, len(backends))
	for _, b := range backends {
		prices[b.ID()] = b.CostPerToken()
	}
	return prices
}
