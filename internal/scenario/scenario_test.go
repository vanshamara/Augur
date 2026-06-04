package scenario

import "testing"

func TestEveryProductPromiseHolds(t *testing.T) {
	results := Run()
	if len(results) != 6 {
		t.Fatalf("expected 6 scenarios, got %d", len(results))
	}
	for _, result := range results {
		if !result.OK {
			t.Errorf("scenario %q did not hold: %s", result.Name, result.Summary)
		}
	}
}

func TestScenariosAreReproducible(t *testing.T) {
	first := Run()
	second := Run()
	for i := range first {
		if first[i].Summary != second[i].Summary {
			t.Errorf("scenario %q changed between runs:\n %s\n %s", first[i].Name, first[i].Summary, second[i].Summary)
		}
	}
}
