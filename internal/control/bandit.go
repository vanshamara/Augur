package control

import (
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

type ShadowFunc func(req core.Request, id core.BackendID, at time.Time) (core.Response, float64, bool)

type BanditConfig struct {
	Policy         *Policy
	Backends       []core.BackendID
	Clock          clock.Clock
	Seed           uint64
	Tau            time.Duration
	PriorPrecision float64
	StatsWindow    int
	Shadow         ShadowFunc
}

type BanditRouter struct {
	policy      *Policy
	clock       clock.Clock
	deriver     *rng.Deriver
	gate        *Gate
	stats       *StatTracker
	reward      *LinearModel
	quality     *QualityModel
	attribution *AttributionLog
	shadow      ShadowFunc
}

func NewBanditRouter(config BanditConfig) *BanditRouter {
	if config.Policy == nil {
		config.Policy = NewPolicy(PolicyConfig{})
	}
	if config.Clock == nil {
		config.Clock = clock.NewReal()
	}
	if config.Tau <= 0 {
		config.Tau = time.Minute
	}
	if config.PriorPrecision <= 0 {
		config.PriorPrecision = 1
	}

	linearConfig := LinearConfig{
		Backends:       config.Backends,
		Dimension:      FeatureDimension,
		Start:          config.Clock.Now(),
		Tau:            config.Tau,
		PriorPrecision: config.PriorPrecision,
	}

	return &BanditRouter{
		policy:      config.Policy,
		clock:       config.Clock,
		deriver:     rng.NewDeriver(config.Seed),
		gate:        NewGate(config.Policy),
		stats:       NewStatTracker(config.Backends, config.StatsWindow),
		reward:      NewLinearModel(linearConfig),
		quality:     NewQualityModel(linearConfig),
		attribution: NewAttributionLog(),
		shadow:      config.Shadow,
	}
}

func (b *BanditRouter) Name() string {
	return "bandit"
}

func (b *BanditRouter) Pick(req core.Request, candidates []core.BackendID) core.BackendID {
	at := b.clock.Now()
	features := EncodeFeatures(req)
	decision := b.gate.Filter(req, candidates, b.stats.Snapshot(), b.quality, at)
	if len(decision.Candidates) == 0 {
		return ""
	}

	chosen := b.choose(req, features, decision.Candidates, at)
	judgingPropensity := b.judgingPropensity(req, chosen, at)
	shadows := b.applyShadow(req, chosen, decision.Candidates, at)

	b.attribution.RecordDecision(DecisionRecord{
		RequestID:          req.ID,
		Backend:            chosen,
		Features:           features,
		PolicyID:           b.policy.ID(),
		Strategy:           b.Name(),
		RoutingPropensity:  1 / float64(len(decision.Candidates)),
		JudgingPropensity:  judgingPropensity,
		At:                 at,
		ShadowBackends:     shadows,
		InfeasibleFallback: decision.Infeasible,
	})
	return chosen
}

func (b *BanditRouter) Observe(choice core.BackendID, resp core.Response) {
	at := b.clock.Now()
	b.stats.Observe(choice, resp)
	b.attribution.RecordResponse(ResponseRecord{RequestID: resp.RequestID, Response: resp, At: at})

	record, ok := b.findDecision(resp)
	if !ok {
		record = DecisionRecord{
			Backend:  choice,
			Features: make([]float64, FeatureDimension),
			At:       at,
		}
		record.Features[0] = 1
	}

	b.reward.Update(LinearObservation{
		Backend:      choice,
		Features:     record.Features,
		Value:        b.policy.Reward(resp),
		Weight:       1,
		At:           at,
		DecisionTime: record.At,
	})
}

func (b *BanditRouter) ObserveQuality(requestID string, score float64) bool {
	record, ok := b.attribution.Decision(requestID)
	if !ok {
		return false
	}

	propensity := record.JudgingPropensity
	if propensity <= 0 {
		propensity = 1
	}

	b.quality.Update(LinearObservation{
		Backend:      record.Backend,
		Features:     record.Features,
		Value:        score,
		Weight:       1 / propensity,
		At:           b.clock.Now(),
		DecisionTime: record.At,
	})
	return true
}

func (b *BanditRouter) Attribution() *AttributionLog {
	return b.attribution
}

func (b *BanditRouter) RewardModel() *LinearModel {
	return b.reward
}

func (b *BanditRouter) QualityModel() *QualityModel {
	return b.quality
}

func (b *BanditRouter) Stats() *StatTracker {
	return b.stats
}

func (b *BanditRouter) Flush() {
	b.reward.Flush()
	b.quality.Flush()
}

func (b *BanditRouter) Close() {
	b.reward.Close()
	b.quality.Close()
}

func (b *BanditRouter) choose(req core.Request, features []float64, candidates []core.BackendID, at time.Time) core.BackendID {
	best := candidates[0]
	bestScore := b.sample(req, best, features, at)
	for _, id := range candidates[1:] {
		score := b.sample(req, id, features, at)
		if score > bestScore {
			best = id
			bestScore = score
		}
	}
	return best
}

func (b *BanditRouter) sample(req core.Request, id core.BackendID, features []float64, at time.Time) float64 {
	return b.reward.Sample(
		id,
		features,
		at,
		b.deriver,
		rng.HashKey(req.ID),
		rng.HashKey(string(id)),
		rng.HashKey("bandit"),
	)
}

func (b *BanditRouter) judgingPropensity(req core.Request, id core.BackendID, at time.Time) float64 {
	rate := b.policy.config.Exploration.JudgeSampleRate
	if rate <= 0 {
		return 0
	}
	if b.policy.config.Exploration.UncertaintySampling {
		prediction := b.quality.Predict(id, req, at)
		if prediction.Variance > 0.1 {
			rate *= 2
		}
	}
	if rate > 1 {
		return 1
	}
	return rate
}

func (b *BanditRouter) applyShadow(req core.Request, chosen core.BackendID, candidates []core.BackendID, at time.Time) []core.BackendID {
	if b.shadow == nil {
		return nil
	}

	var shadows []core.BackendID
	features := EncodeFeatures(req)
	for _, id := range candidates {
		if id == chosen {
			continue
		}
		resp, quality, ok := b.shadow(req, id, at)
		if !ok {
			continue
		}
		shadows = append(shadows, id)
		b.reward.Update(LinearObservation{
			Backend:      id,
			Features:     features,
			Value:        b.policy.Reward(resp),
			Weight:       1,
			At:           at,
			DecisionTime: at,
		})
		if quality >= 0 {
			b.quality.Update(LinearObservation{
				Backend:      id,
				Features:     features,
				Value:        quality,
				Weight:       1,
				At:           at,
				DecisionTime: at,
			})
		}
	}
	return shadows
}

func (b *BanditRouter) findDecision(resp core.Response) (DecisionRecord, bool) {
	if record, ok := b.attribution.Decision(resp.RequestID); ok {
		return record, true
	}
	return DecisionRecord{}, false
}
