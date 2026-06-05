package httpapi

import "testing"

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	limiter := NewRateLimiter(RateSpec{RequestsPerSecond: 0.001, Burst: 2}, nil)

	if !limiter.Allow("tenant-a") {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow("tenant-a") {
		t.Fatal("second request should fit the burst")
	}
	if limiter.Allow("tenant-a") {
		t.Fatal("third request should be blocked once the burst is spent")
	}
}

func TestRateLimiterIsolatesIdentities(t *testing.T) {
	limiter := NewRateLimiter(RateSpec{RequestsPerSecond: 0.001, Burst: 1}, nil)

	if !limiter.Allow("tenant-a") {
		t.Fatal("tenant-a first request should be allowed")
	}
	if limiter.Allow("tenant-a") {
		t.Fatal("tenant-a second request should be blocked")
	}
	if !limiter.Allow("tenant-b") {
		t.Fatal("tenant-b has its own bucket and should be allowed")
	}
}

func TestRateLimiterAppliesTenantOverride(t *testing.T) {
	limiter := NewRateLimiter(
		RateSpec{RequestsPerSecond: 0.001, Burst: 1},
		map[string]RateSpec{"premium": {RequestsPerSecond: 0.001, Burst: 3}},
	)

	for i := 0; i < 3; i++ {
		if !limiter.Allow("premium") {
			t.Fatalf("premium request %d should fit its larger burst", i+1)
		}
	}
	if limiter.Allow("premium") {
		t.Fatal("premium should block after its own burst is spent")
	}
	if !limiter.Allow("standard") {
		t.Fatal("standard tenant uses the default burst and should be allowed once")
	}
	if limiter.Allow("standard") {
		t.Fatal("standard tenant should block on its second request")
	}
}

func TestRateLimiterWithoutRateAllows(t *testing.T) {
	limiter := NewRateLimiter(RateSpec{}, nil)
	for i := 0; i < 5; i++ {
		if !limiter.Allow("tenant-a") {
			t.Fatal("no configured rate should allow every request")
		}
	}
}

func TestNilRateLimiterAllows(t *testing.T) {
	var limiter *RateLimiter
	if !limiter.Allow("tenant-a") {
		t.Fatal("a nil limiter should allow all requests")
	}
}
