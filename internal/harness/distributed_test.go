package harness

import "testing"

func TestDistributedLearningAxesCoverReplicaAndSharingModes(t *testing.T) {
	reports := DistributedAxes(StableRegime(), 7, 200, start)
	if len(reports) != 3 {
		t.Fatalf("expected three distributed axes, got %d", len(reports))
	}

	foundSingle := false
	foundIndependent := false
	foundShared := false
	for _, report := range reports {
		if report.Requests != 200 {
			t.Fatalf("request count got %d want 200", report.Requests)
		}
		if report.MeanPosteriorKL < 0 {
			t.Fatalf("posterior KL should not be negative, got %v", report.MeanPosteriorKL)
		}
		if report.ReplicaCount == 1 && !report.Sharing {
			foundSingle = true
		}
		if report.ReplicaCount == 4 && !report.Sharing {
			foundIndependent = true
		}
		if report.ReplicaCount == 4 && report.Sharing {
			foundShared = true
		}
	}

	if !foundSingle || !foundIndependent || !foundShared {
		t.Fatalf("missing expected axes: single=%v independent=%v shared=%v", foundSingle, foundIndependent, foundShared)
	}
}

func TestDistributedLearningStoreDownStillReports(t *testing.T) {
	report := RunDistributedLearning(StableRegime(), 7, 200, start, DistributedLearningConfig{
		ReplicaCount: 4,
		Sharing:      true,
		StoreDown:    true,
	})

	if !report.StoreDown {
		t.Fatal("report should preserve the store-down setting")
	}
	if report.Requests != 200 {
		t.Fatalf("request count got %d want 200", report.Requests)
	}
	if report.CumulativeLearningCost < 0 {
		t.Fatalf("learning cost should not be negative, got %v", report.CumulativeLearningCost)
	}
}
