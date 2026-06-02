package control

type SLOSnapshot struct {
	Samples   int
	P95Ms     float64
	ErrorRate float64
	Quality   float64
}

type RollbackConfig struct {
	P95RegressionRatio float64
	MaxErrorRate       float64
	MinQuality         float64
	MinSamples         int
}

type RollbackGuard struct {
	config RollbackConfig
}

func NewRollbackGuard(config RollbackConfig) *RollbackGuard {
	if config.P95RegressionRatio <= 0 {
		config.P95RegressionRatio = 0.20
	}
	if config.MaxErrorRate <= 0 {
		config.MaxErrorRate = 0.02
	}
	if config.MinSamples <= 0 {
		config.MinSamples = 20
	}
	return &RollbackGuard{config: config}
}

func (r *RollbackGuard) Config() RollbackConfig {
	return r.config
}

func (r *RollbackGuard) ShouldRollback(baseline SLOSnapshot, canary SLOSnapshot) bool {
	if canary.Samples > 0 && canary.Samples < r.config.MinSamples {
		return false
	}
	if baseline.P95Ms > 0 && canary.P95Ms > baseline.P95Ms*(1+r.config.P95RegressionRatio) {
		return true
	}
	if canary.ErrorRate > r.config.MaxErrorRate {
		return true
	}
	if r.config.MinQuality > 0 && canary.Quality < r.config.MinQuality {
		return true
	}
	return false
}
