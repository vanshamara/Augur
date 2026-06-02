package control

import (
	"math"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/learn"
	"github.com/vanshamara/Augur/internal/rng"
)

type LinearConfig struct {
	Backends       []core.BackendID
	Dimension      int
	Start          time.Time
	Tau            time.Duration
	PriorPrecision float64
	InitialMean    float64
}

type LinearObservation struct {
	Backend      core.BackendID
	Features     []float64
	Value        float64
	Weight       float64
	At           time.Time
	DecisionTime time.Time
}

type LinearModel struct {
	core           *learn.Core[LinearSnapshot, LinearObservation]
	tau            time.Duration
	priorPrecision float64
	initialMean    float64
	dimension      int
}

type LinearSnapshot struct {
	Arms map[core.BackendID]LinearArm
}

type LinearArm struct {
	Precision []float64
	Target    []float64
	Last      time.Time
	Updates   float64
}

func NewLinearModel(config LinearConfig) *LinearModel {
	if config.Dimension <= 0 {
		config.Dimension = FeatureDimension
	}
	if config.Tau <= 0 {
		config.Tau = time.Minute
	}
	if config.PriorPrecision <= 0 {
		config.PriorPrecision = 1
	}

	initial := LinearSnapshot{Arms: map[core.BackendID]LinearArm{}}
	for _, id := range config.Backends {
		initial.Arms[id] = newArm(config.Dimension, config.Start)
	}

	model := &LinearModel{
		tau:            config.Tau,
		priorPrecision: config.PriorPrecision,
		initialMean:    config.InitialMean,
		dimension:      config.Dimension,
	}
	model.core = learn.NewCore(initial, model.apply)
	return model
}

func (m *LinearModel) Update(observation LinearObservation) {
	if observation.Weight <= 0 {
		observation.Weight = 1
	}
	m.core.Update(observation)
}

func (m *LinearModel) Snapshot() LinearSnapshot {
	return m.core.Snapshot()
}

func (m *LinearModel) Restore(snapshot LinearSnapshot) {
	m.core.Transform(func(current LinearSnapshot) LinearSnapshot {
		return snapshot.clone(m.dimension)
	})
}

func (m *LinearModel) Predict(id core.BackendID, features []float64, at time.Time) Prediction {
	return m.Snapshot().Predict(id, features, at, m.tau, m.priorPrecision, m.initialMean, false)
}

func (m *LinearModel) Sample(id core.BackendID, features []float64, at time.Time, deriver *rng.Deriver, keys ...uint64) float64 {
	return m.Snapshot().Sample(id, features, at, m.tau, m.priorPrecision, m.initialMean, deriver, keys...)
}

func (m *LinearModel) Flush() {
	m.core.Flush()
}

func (m *LinearModel) Close() {
	m.core.Close()
}

func (m *LinearModel) apply(current LinearSnapshot, observation LinearObservation) LinearSnapshot {
	return applyObservationToSnapshot(current, observation, m.dimension, m.tau)
}

func applyObservationToSnapshot(current LinearSnapshot, observation LinearObservation, dimension int, tau time.Duration) LinearSnapshot {
	next := current.clone(dimension)
	arm := next.arm(observation.Backend, dimension, observation.At)
	arm = decayArm(arm, observation.At, tau)

	weight := observation.Weight
	if !observation.DecisionTime.IsZero() {
		age := observation.At.Sub(observation.DecisionTime)
		if age > 0 {
			weight *= math.Exp(-age.Seconds() / tau.Seconds())
		}
	}

	for i := 0; i < dimension && i < len(observation.Features); i++ {
		value := observation.Features[i]
		arm.Precision[i] += weight * value * value
		arm.Target[i] += weight * value * observation.Value
	}
	arm.Updates += weight
	arm.Last = observation.At
	next.Arms[observation.Backend] = arm
	return next
}

func (s LinearSnapshot) Predict(id core.BackendID, features []float64, at time.Time, tau time.Duration, priorPrecision float64, initialMean float64, clip bool) Prediction {
	arm, ok := s.Arms[id]
	if !ok {
		arm = newArm(len(features), at)
	}
	arm = decayArm(arm, at, tau)

	mean := 0.0
	variance := 0.0
	for i := 0; i < len(features) && i < len(arm.Precision); i++ {
		denominator := priorPrecision + arm.Precision[i]
		numerator := arm.Target[i]
		if i == 0 {
			numerator += priorPrecision * initialMean
		}
		theta := numerator / denominator
		mean += theta * features[i]
		variance += features[i] * features[i] / denominator
	}
	if clip {
		mean = clamp01(mean)
	}
	return Prediction{Mean: mean, Variance: variance, Count: arm.Updates}
}

func (s LinearSnapshot) Sample(id core.BackendID, features []float64, at time.Time, tau time.Duration, priorPrecision float64, initialMean float64, deriver *rng.Deriver, keys ...uint64) float64 {
	arm, ok := s.Arms[id]
	if !ok {
		arm = newArm(len(features), at)
	}
	arm = decayArm(arm, at, tau)

	generator := deriver.Rand(keys...)
	score := 0.0
	for i := 0; i < len(features) && i < len(arm.Precision); i++ {
		denominator := priorPrecision + arm.Precision[i]
		numerator := arm.Target[i]
		if i == 0 {
			numerator += priorPrecision * initialMean
		}
		theta := numerator / denominator
		theta += normal(generator.Float64, generator.Float64) / math.Sqrt(denominator)
		score += theta * features[i]
	}
	return score
}

func (s LinearSnapshot) clone(dimension int) LinearSnapshot {
	out := LinearSnapshot{Arms: make(map[core.BackendID]LinearArm, len(s.Arms))}
	for id, arm := range s.Arms {
		out.Arms[id] = arm.clone(dimension)
	}
	return out
}

func (s LinearSnapshot) arm(id core.BackendID, dimension int, at time.Time) LinearArm {
	arm, ok := s.Arms[id]
	if !ok {
		return newArm(dimension, at)
	}
	return arm.clone(dimension)
}

func (a LinearArm) clone(dimension int) LinearArm {
	if dimension < len(a.Precision) {
		dimension = len(a.Precision)
	}
	precision := make([]float64, dimension)
	target := make([]float64, dimension)
	copy(precision, a.Precision)
	copy(target, a.Target)
	return LinearArm{Precision: precision, Target: target, Last: a.Last, Updates: a.Updates}
}

func newArm(dimension int, at time.Time) LinearArm {
	return LinearArm{
		Precision: make([]float64, dimension),
		Target:    make([]float64, dimension),
		Last:      at,
	}
}

func decayArm(arm LinearArm, at time.Time, tau time.Duration) LinearArm {
	if arm.Last.IsZero() || !at.After(arm.Last) {
		return arm
	}
	factor := math.Exp(-at.Sub(arm.Last).Seconds() / tau.Seconds())
	for i := range arm.Precision {
		arm.Precision[i] *= factor
		arm.Target[i] *= factor
	}
	arm.Updates *= factor
	arm.Last = at
	return arm
}

type randFloat func() float64

func normal(first randFloat, second randFloat) float64 {
	u1 := first()
	if u1 <= 0 {
		u1 = math.SmallestNonzeroFloat64
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*second())
}
