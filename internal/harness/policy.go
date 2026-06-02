package harness

import "github.com/vanshamara/Augur/internal/control"

func DefaultComparisonPolicy() *control.Policy {
	return control.NewPolicy(control.PolicyConfig{
		ID: "comparison",
		Constraints: control.ConstraintConfig{
			MaxP95Ms:     1200,
			MinQuality:   0.85,
			MaxErrorRate: 0.10,
		},
		Objective: control.ObjectiveConfig{
			Type: control.MinimizeLatency,
		},
		OnInfeasible: control.InfeasibleBestEffort,
	})
}
