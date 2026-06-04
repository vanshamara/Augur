package observability

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

// TestPrometheusHandlerExposesRecordedMetrics records one response and checks
// that the scrape output includes the request and cost metrics with no
// per-request label.
func TestPrometheusHandlerExposesRecordedMetrics(t *testing.T) {
	observer, handler, err := NewPrometheus("augur")
	if err != nil {
		t.Fatalf("new prometheus observer: %v", err)
	}

	observer.RecordResponse(context.Background(), core.Response{
		RequestID: "req-1",
		RouteName: "default",
		Backend:   "backend-a",
		Outcome:   core.Outcome{LatencyMs: 120, CostUSD: 0.002},
	}, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	for _, name := range []string{"augur_requests_total", "augur_cost_usd_total", "augur_latency_ms_bucket"} {
		if !strings.Contains(body, name) {
			t.Fatalf("metrics output missing %q", name)
		}
	}
	if strings.Contains(body, "req-1") {
		t.Fatalf("metrics must not carry the request id as a label")
	}
}
