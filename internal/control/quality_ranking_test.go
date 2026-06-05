package control

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

// TestQualityModelRecoversRanking trains the online quality model on synthetic
// labels where true quality depends on (backend, request type), then checks that
// it recovers the correct quality ordering across all backend/type cells. The
// model rescales targets internally, so absolute calibration is not meaningful;
// the ordering is what routing actually depends on. Synthetic, not production.
func TestQualityModelRecoversRanking(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := []core.BackendID{"cheap", "mid", "strong"}
	types := []core.RequestType{core.Chat, core.Coding, core.Reasoning}

	truth := map[core.BackendID]map[core.RequestType]float64{
		"cheap":  {core.Chat: 0.95, core.Coding: 0.50, core.Reasoning: 0.60},
		"mid":    {core.Chat: 0.90, core.Coding: 0.75, core.Reasoning: 0.80},
		"strong": {core.Chat: 0.88, core.Coding: 0.96, core.Reasoning: 0.92},
	}

	model := NewQualityModel(LinearConfig{
		Backends:       ids,
		Dimension:      FeatureDimension,
		Start:          start,
		Tau:            time.Hour,
		PriorPrecision: 1,
	})
	defer model.Close()

	gen := rand.New(rand.NewPCG(1, 2))
	const observations = 6000
	for i := 0; i < observations; i++ {
		id := ids[gen.IntN(len(ids))]
		typ := types[gen.IntN(len(types))]
		q := clamp01(truth[id][typ] + (gen.Float64()-0.5)*0.1)
		req := core.Request{ID: "train", Features: core.Features{Type: typ}}
		model.Update(LinearObservation{
			Backend:      id,
			Features:     EncodeFeatures(req),
			Value:        q,
			Weight:       1,
			At:           start,
			DecisionTime: start,
		})
	}

	type cell struct {
		id    core.BackendID
		typ   core.RequestType
		truth float64
		pred  float64
	}
	var cells []cell
	for _, id := range ids {
		for _, typ := range types {
			req := core.Request{ID: "eval", Features: core.Features{Type: typ}}
			pred := model.Predict(id, req, start).Mean
			cells = append(cells, cell{id, typ, truth[id][typ], pred})
			t.Logf("%-7s %-9s true=%.2f pred=%.3f", id, typ, truth[id][typ], pred)
		}
	}

	correct, total := 0, 0
	for i := 0; i < len(cells); i++ {
		for j := i + 1; j < len(cells); j++ {
			total++
			if (cells[i].truth < cells[j].truth) == (cells[i].pred < cells[j].pred) {
				correct++
			}
		}
	}
	accuracy := float64(correct) / float64(total)
	t.Logf("pairwise ranking accuracy: %d/%d = %.1f%%", correct, total, accuracy*100)

	if accuracy < 0.85 {
		t.Fatalf("ranking accuracy too low: %.1f%%", accuracy*100)
	}
}
