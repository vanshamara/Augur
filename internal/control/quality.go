package control

import (
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

type QualityModel struct {
	model *LinearModel
}

func NewQualityModel(config LinearConfig) *QualityModel {
	if config.InitialMean == 0 {
		config.InitialMean = 0.9
	}
	return &QualityModel{model: NewLinearModel(config)}
}

func (q *QualityModel) Update(observation LinearObservation) {
	observation.Value = clamp01(observation.Value)
	q.model.Update(observation)
}

func (q *QualityModel) Predict(id core.BackendID, req core.Request, at time.Time) Prediction {
	features := EncodeFeatures(req)
	return q.PredictFeatures(id, features, at)
}

func (q *QualityModel) PredictFeatures(id core.BackendID, features []float64, at time.Time) Prediction {
	snapshot := q.model.Snapshot()
	return snapshot.Predict(id, features, at, q.model.tau, q.model.priorPrecision, q.model.initialMean, true)
}

func (q *QualityModel) Flush() {
	q.model.Flush()
}

func (q *QualityModel) Close() {
	q.model.Close()
}
