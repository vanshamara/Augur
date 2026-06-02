package control

type SLOSnapshot struct {
	P95Ms     float64
	ErrorRate float64
	Quality   float64
}

type RollbackConfig struct {
	P95RegressionRatio float64
	MaxErrorRate       float64
	MinQuality         float64
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
	return &RollbackGuard{config: config}
}

// ShouldRollback checks the canary against stubbed SLO thresholds.
func (r *RollbackGuard) ShouldRollback(baseline SLOSnapshot, canary SLOSnapshot) bool {
	// TODO: tune with real traffic.
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
