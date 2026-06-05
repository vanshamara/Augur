package httpapi

import (
	"sync"

	"golang.org/x/time/rate"
)

// RateSpec is one rate limit: a steady requests-per-second rate and a burst.
type RateSpec struct {
	RequestsPerSecond float64
	Burst             int
}

// RateLimiter applies a token-bucket request rate limit per identity. It uses a
// default rate, with optional per-identity overrides. One bucket is kept per
// identity, so the map stays bounded by the configured identity set.
type RateLimiter struct {
	def       RateSpec
	overrides map[string]RateSpec
	mu        sync.Mutex
	buckets   map[string]*rate.Limiter
}

func NewRateLimiter(def RateSpec, overrides map[string]RateSpec) *RateLimiter {
	copied := make(map[string]RateSpec, len(overrides))
	for identity, spec := range overrides {
		copied[identity] = spec
	}
	return &RateLimiter{
		def:       def,
		overrides: copied,
		buckets:   map[string]*rate.Limiter{},
	}
}

// Allow reports whether the identity may make one more request right now. An
// identity with no override and no default rate is never limited.
func (l *RateLimiter) Allow(identity string) bool {
	if l == nil {
		return true
	}
	spec, ok := l.overrides[identity]
	if !ok {
		spec = l.def
	}
	if spec.RequestsPerSecond <= 0 {
		return true
	}

	l.mu.Lock()
	bucket, ok := l.buckets[identity]
	if !ok {
		burst := spec.Burst
		if burst < 1 {
			burst = 1
		}
		bucket = rate.NewLimiter(rate.Limit(spec.RequestsPerSecond), burst)
		l.buckets[identity] = bucket
	}
	l.mu.Unlock()
	return bucket.Allow()
}
