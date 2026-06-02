package control

import (
	"math"

	"github.com/vanshamara/Augur/internal/core"
)

type ObjectiveType string

const (
	MinimizeCost    ObjectiveType = "minimize_cost"
	MinimizeLatency ObjectiveType = "minimize_latency"
	BlendObjective  ObjectiveType = "blend"
)

type GateStatistic string

const (
	GateOnMean GateStatistic = "mean"
	GateOnLCB  GateStatistic = "lcb"
	GateOnUCB  GateStatistic = "ucb"
)

type InfeasibleAction string

const (
	InfeasibleBestEffort InfeasibleAction = "best_effort"
	InfeasibleFailClosed InfeasibleAction = "fail_closed"
)

type ConstraintConfig struct {
	MaxP95Ms     float64       `json:"max_p95_ms"`
	MinQuality   float64       `json:"min_quality"`
	MaxErrorRate float64       `json:"max_error_rate"`
	QualityGate  GateStatistic `json:"quality_gate"`
}

type ObjectiveConfig struct {
	Type          ObjectiveType `json:"type"`
	LatencyWeight float64       `json:"latency_weight"`
	CostWeight    float64       `json:"cost_weight"`
}

type ExplorationConfig struct {
	ColdStartBudget     float64 `json:"cold_start_budget"`
	JudgeSampleRate     float64 `json:"judge_sample_rate"`
	UncertaintySampling bool    `json:"uncertainty_sampling"`
}

type PolicyConfig struct {
	ID           string            `json:"id"`
	Constraints  ConstraintConfig  `json:"constraints"`
	Objective    ObjectiveConfig   `json:"objective"`
	Exploration  ExplorationConfig `json:"exploration"`
	OnInfeasible InfeasibleAction  `json:"on_infeasible"`
}

type Policy struct {
	config PolicyConfig
}

func NewPolicy(config PolicyConfig) *Policy {
	if config.ID == "" {
		config.ID = "default"
	}
	if config.Objective.Type == "" {
		config.Objective.Type = MinimizeLatency
	}
	if config.Objective.Type == BlendObjective {
		if config.Objective.LatencyWeight == 0 {
			config.Objective.LatencyWeight = 1
		}
		if config.Objective.CostWeight == 0 {
			config.Objective.CostWeight = 1
		}
	}
	if config.Constraints.QualityGate == "" {
		config.Constraints.QualityGate = GateOnMean
	}
	if config.OnInfeasible == "" {
		config.OnInfeasible = InfeasibleBestEffort
	}
	return &Policy{config: config}
}

func (p *Policy) ID() string {
	return p.config.ID
}

func (p *Policy) Config() PolicyConfig {
	return p.config
}

// Reward returns the objective reward for a completed response.
func (p *Policy) Reward(resp core.Response) float64 {
	if resp.Errored {
		return BadOutcomeReward
	}
	return -p.ObjectiveCost(resp)
}

func (p *Policy) ObjectiveCost(resp core.Response) float64 {
	switch p.config.Objective.Type {
	case MinimizeCost:
		return resp.CostUSD
	case BlendObjective:
		latency := p.config.Objective.LatencyWeight * resp.LatencyMs
		cost := p.config.Objective.CostWeight * resp.CostUSD * 1_000_000
		return latency + cost
	default:
		return resp.LatencyMs
	}
}

const BadOutcomeReward = -1_000_000_000

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func confidenceWidth(variance float64) float64 {
	if variance <= 0 {
		return 0
	}
	return 2 * math.Sqrt(variance)
}
