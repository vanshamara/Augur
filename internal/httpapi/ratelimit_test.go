package httpapi

import "testing"

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	limiter := NewRateLimiter(0.001, 2)

	if !limiter.Allow("key-a") {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow("key-a") {
		t.Fatal("second request should fit the burst")
	}
	if limiter.Allow("key-a") {
		t.Fatal("third request should be blocked once the burst is spent")
	}
}

func TestRateLimiterIsolatesIdentities(t *testing.T) {
	limiter := NewRateLimiter(0.001, 1)

	if !limiter.Allow("key-a") {
		t.Fatal("key-a first request should be allowed")
	}
	if limiter.Allow("key-a") {
		t.Fatal("key-a second request should be blocked")
	}
	if !limiter.Allow("key-b") {
		t.Fatal("key-b has its own bucket and should be allowed")
	}
}

func TestNilRateLimiterAllows(t *testing.T) {
	var limiter *RateLimiter
	if !limiter.Allow("key-a") {
		t.Fatal("a nil limiter should allow all requests")
	}
}
