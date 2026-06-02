package harness

import (
	"math"
	"testing"
)

func TestMeanAndStdDev(t *testing.T) {
	values := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	m := mean(values)
	if m != 5 {
		t.Fatalf("expected mean 5, got %v", m)
	}
	if sd := sampleStdDev(values, m); math.Abs(sd-2.138) > 0.01 {
		t.Fatalf("expected sample stddev near 2.138, got %v", sd)
	}
}

func TestCIHalfWidthShrinksWithMoreData(t *testing.T) {
	small := ciHalfWidth([]float64{1, 2, 3, 4, 5})
	large := []float64{}
	for i := 0; i < 100; i++ {
		large = append(large, float64(1+i%5))
	}
	if ciHalfWidth(large) >= small {
		t.Fatal("more samples of the same spread should give a tighter interval")
	}
}

func TestPairedDiffSubtractsBySeed(t *testing.T) {
	diffs := pairedDiff([]float64{10, 20, 30}, []float64{1, 2, 3})
	want := []float64{9, 18, 27}
	for i := range want {
		if diffs[i] != want[i] {
			t.Fatalf("paired diff %d: got %v want %v", i, diffs[i], want[i])
		}
	}
}
