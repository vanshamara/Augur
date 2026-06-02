package control

import (
	"math"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

func EmptyLinearSnapshot(backends []core.BackendID, dimension int, at time.Time) LinearSnapshot {
	snapshot := LinearSnapshot{Arms: map[core.BackendID]LinearArm{}}
	for _, id := range backends {
		snapshot.Arms[id] = newArm(dimension, at)
	}
	return snapshot
}

func (s LinearSnapshot) Decayed(at time.Time, tau time.Duration) LinearSnapshot {
	out := s.clone(0)
	for id, arm := range out.Arms {
		out.Arms[id] = decayArm(arm, at, tau)
	}
	return out
}

func (s LinearSnapshot) Add(delta LinearSnapshot, at time.Time, tau time.Duration) LinearSnapshot {
	out := s.Decayed(at, tau)
	for id, arm := range delta.Decayed(at, tau).Arms {
		current, ok := out.Arms[id]
		if !ok {
			out.Arms[id] = arm.clone(len(arm.Precision))
			continue
		}
		out.Arms[id] = addArms(current, arm, at)
	}
	return out
}

func (s LinearSnapshot) TotalUpdates(at time.Time, tau time.Duration) float64 {
	total := 0.0
	for _, arm := range s.Decayed(at, tau).Arms {
		total += arm.Updates
	}
	return total
}

func (s LinearSnapshot) BestArm(candidates []core.BackendID, features []float64, at time.Time, tau time.Duration, priorPrecision float64, initialMean float64) core.BackendID {
	if len(candidates) == 0 {
		return ""
	}

	best := candidates[0]
	bestPrediction := s.Predict(best, features, at, tau, priorPrecision, initialMean, false).Mean
	for _, id := range candidates[1:] {
		prediction := s.Predict(id, features, at, tau, priorPrecision, initialMean, false).Mean
		if prediction > bestPrediction {
			best = id
			bestPrediction = prediction
		}
	}
	return best
}

func SymmetricKL(a LinearSnapshot, b LinearSnapshot, at time.Time, tau time.Duration, priorPrecision float64) float64 {
	left := a.Decayed(at, tau)
	right := b.Decayed(at, tau)
	total := 0.0
	count := 0

	for id, leftArm := range left.Arms {
		rightArm, ok := right.Arms[id]
		if !ok {
			continue
		}
		limit := len(leftArm.Precision)
		if len(rightArm.Precision) < limit {
			limit = len(rightArm.Precision)
		}
		for i := 0; i < limit; i++ {
			leftVar := 1 / (priorPrecision + leftArm.Precision[i])
			rightVar := 1 / (priorPrecision + rightArm.Precision[i])
			leftMean := safeMean(leftArm.Target[i], leftArm.Precision[i])
			rightMean := safeMean(rightArm.Target[i], rightArm.Precision[i])
			total += gaussianKL(leftMean, leftVar, rightMean, rightVar)
			total += gaussianKL(rightMean, rightVar, leftMean, leftVar)
			count += 2
		}
	}

	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func addArms(a LinearArm, b LinearArm, at time.Time) LinearArm {
	dimension := len(a.Precision)
	if len(b.Precision) > dimension {
		dimension = len(b.Precision)
	}
	out := newArm(dimension, at)
	for i := 0; i < dimension; i++ {
		if i < len(a.Precision) {
			out.Precision[i] += a.Precision[i]
			out.Target[i] += a.Target[i]
		}
		if i < len(b.Precision) {
			out.Precision[i] += b.Precision[i]
			out.Target[i] += b.Target[i]
		}
	}
	out.Updates = a.Updates + b.Updates
	return out
}

func safeMean(target float64, precision float64) float64 {
	if precision <= 0 {
		return 0
	}
	return target / precision
}

func gaussianKL(meanA float64, varA float64, meanB float64, varB float64) float64 {
	if varA <= 0 || varB <= 0 {
		return 0
	}
	diff := meanB - meanA
	return 0.5 * ((varA+diff*diff)/varB - 1 + math.Log(varB/varA))
}
