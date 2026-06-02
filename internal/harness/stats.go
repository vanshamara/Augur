package harness

import "math"

// Stat is the mean of a set of per seed values and the half width of its 95 percent
// confidence interval. A small half width means more seeds would not move the number
// much.
type Stat struct {
	Mean   float64
	CIHalf float64
}

func statOf(values []float64) Stat {
	return Stat{Mean: mean(values), CIHalf: ciHalfWidth(values)}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func sampleStdDev(values []float64, m float64) float64 {
	if len(values) < 2 {
		return 0
	}
	sumSquares := 0.0
	for _, v := range values {
		diff := v - m
		sumSquares += diff * diff
	}
	return math.Sqrt(sumSquares / float64(len(values)-1))
}

// ciHalfWidth uses the normal approximation, which is fine once there are a few
// dozen seeds. For very small seed counts it slightly understates the interval.
func ciHalfWidth(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	stdErr := sampleStdDev(values, mean(values)) / math.Sqrt(float64(len(values)))
	return 1.96 * stdErr
}

// pairedDiff lines up two routers by seed and returns the per seed difference. With
// common random numbers both routers saw the same draws, so this difference cancels
// the shared luck and is the right thing to put a confidence interval on.
func pairedDiff(a, b []float64) []float64 {
	diffs := make([]float64, len(a))
	for i := range a {
		diffs[i] = a[i] - b[i]
	}
	return diffs
}
