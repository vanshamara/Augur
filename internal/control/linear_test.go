package control

import (
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

func TestLinearModelDecaysDelayedObservationFromDecisionTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	model := NewLinearModel(LinearConfig{
		Backends:       []core.BackendID{"a"},
		Dimension:      FeatureDimension,
		Start:          start,
		Tau:            time.Second,
		PriorPrecision: 1,
	})
	defer model.Close()

	model.Update(LinearObservation{
		Backend:      "a",
		Features:     EncodeFeatures(request("req-1")),
		Value:        100,
		Weight:       1,
		At:           start.Add(10 * time.Second),
		DecisionTime: start,
	})
	model.Flush()

	prediction := model.Predict("a", EncodeFeatures(request("req-2")), start.Add(10*time.Second))
	if prediction.Count >= 0.001 {
		t.Fatalf("old delayed label should land almost fully decayed, count=%v", prediction.Count)
	}
}

func TestQualityModelClipsPrediction(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	quality := NewQualityModel(LinearConfig{
		Backends:       []core.BackendID{"a"},
		Dimension:      FeatureDimension,
		Start:          start,
		Tau:            time.Minute,
		PriorPrecision: 1,
		InitialMean:    0.5,
	})
	defer quality.Close()

	quality.Update(LinearObservation{
		Backend:  "a",
		Features: EncodeFeatures(request("req-1")),
		Value:    2,
		Weight:   10,
		At:       start,
	})
	quality.Flush()

	prediction := quality.Predict("a", request("req-2"), start)
	if prediction.Mean > 1 {
		t.Fatalf("quality prediction should be clipped to one, got %v", prediction.Mean)
	}
}
