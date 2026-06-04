package mock

import (
	"math"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

// Params are the true generative parameters of a backend at a point in time.
// The oracle reads these to know the best possible choice. Real outcomes are
// samples drawn around them.
type Params struct {
	MeanLatencyMs float64
	LatencySpread float64 // fraction of the mean, so 0.2 means give or take 20 percent
	ErrorRate     float64
	Quality       float64 // true mean quality in the range 0 to 1
	CostPerToken  float64
}

// Profile defines how a backend behaves and how that behavior drifts over time.
type Profile struct {
	Name      string
	paramsAt  func(elapsed time.Duration) Params
	paramsFor func(elapsed time.Duration, req core.Request) Params
}

// ParamsAt returns the true parameters after the given time has passed since the
// backend started.
func (p Profile) ParamsAt(elapsed time.Duration) Params {
	return p.paramsAt(elapsed)
}

// ParamsFor returns the true parameters for one request shape.
func (p Profile) ParamsFor(elapsed time.Duration, req core.Request) Params {
	if p.paramsFor != nil {
		return p.paramsFor(elapsed, req)
	}
	return p.ParamsAt(elapsed)
}

// CheapLowerQuality is cheap and fast enough but gives weaker answers.
func CheapLowerQuality() Profile {
	base := Params{MeanLatencyMs: 800, LatencySpread: 0.20, ErrorRate: 0.01, Quality: 0.72, CostPerToken: 0.0000010}
	return steady("cheap-but-lower-quality", base)
}

// FastFlaky answers quickly with good quality but fails more often.
func FastFlaky() Profile {
	base := Params{MeanLatencyMs: 300, LatencySpread: 0.30, ErrorRate: 0.08, Quality: 0.85, CostPerToken: 0.0000050}
	return steady("fast-but-flaky", base)
}

// SlowStable is slow and pricey but rarely fails and gives strong answers.
func SlowStable() Profile {
	base := Params{MeanLatencyMs: 1500, LatencySpread: 0.10, ErrorRate: 0.002, Quality: 0.93, CostPerToken: 0.0000100}
	return steady("slow-but-stable", base)
}

// RisingP99 starts healthy and gets slower as time passes, like a backend under
// growing load.
func RisingP99() Profile {
	return Profile{Name: "rising-p99", paramsAt: func(elapsed time.Duration) Params {
		return Params{
			MeanLatencyMs: 400 + 200*elapsed.Minutes(),
			LatencySpread: 0.25,
			ErrorRate:     0.01,
			Quality:       0.88,
			CostPerToken:  0.0000040,
		}
	}}
}

// Intermittent500s is mostly fine but throws bursts of errors on a cycle.
func Intermittent500s() Profile {
	return Profile{Name: "intermittent-500s", paramsAt: func(elapsed time.Duration) Params {
		errorRate := 0.01
		if math.Mod(elapsed.Seconds(), 60) < 10 {
			errorRate = 0.40
		}
		return Params{MeanLatencyMs: 500, LatencySpread: 0.20, ErrorRate: errorRate, Quality: 0.86, CostPerToken: 0.0000045}
	}}
}

// ColdStart is very slow at first and warms up to a steady speed.
func ColdStart() Profile {
	return Profile{Name: "cold-start", paramsAt: func(elapsed time.Duration) Params {
		warm := math.Exp(-elapsed.Seconds() / 15)
		return Params{
			MeanLatencyMs: 500 + 2500*warm,
			LatencySpread: 0.15,
			ErrorRate:     0.005,
			Quality:       0.90,
			CostPerToken:  0.0000060,
		}
	}}
}

func CheapChatSpecialist() Profile {
	chat := Params{MeanLatencyMs: 220, LatencySpread: 0.12, ErrorRate: 0.01, Quality: 0.88, CostPerToken: 0.0000010}
	hard := Params{MeanLatencyMs: 1800, LatencySpread: 0.15, ErrorRate: 0.02, Quality: 0.86, CostPerToken: 0.0000010}
	return requestAware("cheap-chat-specialist", chat, func(req core.Request) Params {
		switch req.Features.Type {
		case core.Reasoning, core.Coding:
			return hard
		default:
			return chat
		}
	})
}

func BalancedGeneralist() Profile {
	base := Params{MeanLatencyMs: 1100, LatencySpread: 0.10, ErrorRate: 0.01, Quality: 0.90, CostPerToken: 0.0000040}
	return steady("balanced-generalist", base)
}

func StrongReasoningSpecialist() Profile {
	chat := Params{MeanLatencyMs: 1400, LatencySpread: 0.12, ErrorRate: 0.006, Quality: 0.92, CostPerToken: 0.0000080}
	hard := Params{MeanLatencyMs: 260, LatencySpread: 0.10, ErrorRate: 0.004, Quality: 0.96, CostPerToken: 0.0000080}
	return requestAware("strong-reasoning-specialist", chat, func(req core.Request) Params {
		switch req.Features.Type {
		case core.Reasoning, core.Coding:
			return hard
		default:
			return chat
		}
	})
}

func steady(name string, base Params) Profile {
	return Profile{Name: name, paramsAt: func(time.Duration) Params { return base }}
}

func requestAware(name string, fallback Params, paramsFor func(core.Request) Params) Profile {
	return Profile{
		Name:     name,
		paramsAt: func(time.Duration) Params { return fallback },
		paramsFor: func(elapsed time.Duration, req core.Request) Params {
			return paramsFor(req)
		},
	}
}
