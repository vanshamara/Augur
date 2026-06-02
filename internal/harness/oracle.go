package harness

import (
	"math"
	"time"

	"github.com/vanshamara/Augur/internal/backend/mock"
	"github.com/vanshamara/Augur/internal/core"
)

// Oracle answers what the best choice would have been. The expectation oracle uses
// each backend's true mean, which is what a perfectly informed router could match,
// so it measures learning. The realization oracle uses the exact draw for a request
// and is only used to show the band of luck that no router can beat.
type Oracle struct {
	backends []*mock.Backend
}

func NewOracle(backends []*mock.Backend) *Oracle {
	return &Oracle{backends: backends}
}

// ExpectedBestLatency returns the lowest true mean latency across backends at the
// given time.
func (o *Oracle) ExpectedBestLatency(at time.Time) float64 {
	best := math.Inf(1)
	for _, b := range o.backends {
		mean := b.TrueParams(at).MeanLatencyMs
		if mean < best {
			best = mean
		}
	}
	return best
}

// RealizedBestLatency returns the lowest actual latency any backend would have
// produced for this exact request at this time.
func (o *Oracle) RealizedBestLatency(req core.Request, at time.Time) float64 {
	best := math.Inf(1)
	for _, b := range o.backends {
		latency := b.Outcome(req, at).LatencyMs
		if latency < best {
			best = latency
		}
	}
	return best
}
