package harness

import (
	"testing"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/router"
)

func TestRunReportsConstraintViolations(t *testing.T) {
	clk := clock.NewVirtual(start)
	backends := buildBackends(7, clk)
	policy := control.NewPolicy(control.PolicyConfig{
		Constraints: control.ConstraintConfig{
			MinQuality: 0.85,
		},
		Objective: control.ObjectiveConfig{
			Type: control.MinimizeLatency,
		},
	})

	report := RunWithPolicy(GenerateTrace(7, 100, start), router.NewStatic("cheap"), backends, clk, policy)
	if report.ConstraintViolationRate != 1 {
		t.Fatalf("cheap backend should violate quality on every request, got %v", report.ConstraintViolationRate)
	}
	if report.FeasibleObjectiveCount != 0 {
		t.Fatalf("infeasible choices should not enter feasible objective regret, got %d", report.FeasibleObjectiveCount)
	}
}

func TestRunReportsObjectiveRegretForFeasibleChoices(t *testing.T) {
	clk := clock.NewVirtual(start)
	backends := buildBackends(7, clk)
	policy := control.NewPolicy(control.PolicyConfig{
		Constraints: control.ConstraintConfig{
			MinQuality: 0.85,
		},
		Objective: control.ObjectiveConfig{
			Type: control.MinimizeLatency,
		},
	})

	report := RunWithPolicy(GenerateTrace(7, 100, start), router.NewStatic("stable"), backends, clk, policy)
	if report.ConstraintViolationRate != 0 {
		t.Fatalf("stable backend should satisfy the quality policy, got %v", report.ConstraintViolationRate)
	}
	if report.FeasibleObjectiveCount != report.Count {
		t.Fatalf("every stable choice should be feasible, got %d of %d", report.FeasibleObjectiveCount, report.Count)
	}
	if report.ObjectiveRegretFeasible <= 0 {
		t.Fatalf("stable should be slower than the best feasible backend, got %v", report.ObjectiveRegretFeasible)
	}
	if report.CostOfLearning <= 0 {
		t.Fatalf("slower feasible choices should create learning cost, got %v", report.CostOfLearning)
	}
}

func TestCompareIncludesPolicySplit(t *testing.T) {
	comparison := compareStable()
	for _, stats := range comparison.Routers {
		if stats.Router == "cost-aware" && stats.ConstraintViolationRate.Mean == 0 {
			t.Fatal("cost-aware should show quality violations under the comparison policy")
		}
		if stats.Router == "static" && stats.CostOfLearning.Mean == 0 {
			t.Fatal("static cheap routing should show learning cost under the comparison policy")
		}
	}
}
