package httpapi

import (
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter applies a token-bucket request rate limit per client identity. It
// keeps one bucket per identity, so the map stays bounded by the set of
// configured client keys.
type RateLimiter struct {
	limit   rate.Limit
	burst   int
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

func NewRateLimiter(requestsPerSecond float64, burst int) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		limit:   rate.Limit(requestsPerSecond),
		burst:   burst,
		buckets: map[string]*rate.Limiter{},
	}
}

// Allow reports whether the identity may make one more request right now.
func (l *RateLimiter) Allow(identity string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	bucket, ok := l.buckets[identity]
	if !ok {
		bucket = rate.NewLimiter(l.limit, l.burst)
		l.buckets[identity] = bucket
	}
	l.mu.Unlock()
	return bucket.Allow()
}
