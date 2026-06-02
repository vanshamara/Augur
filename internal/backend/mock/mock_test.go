package mock

import (
	"math"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newBackend(id core.BackendID, profile Profile, seed uint64) *Backend {
	return New(id, profile, start, rng.NewDeriver(seed), clock.NewVirtual(start))
}

func request(id string, promptTokens int) core.Request {
	return core.Request{ID: id, Features: core.Features{PromptTokens: promptTokens, Type: core.Chat}}
}

func TestOutcomeIsStableAcrossCallers(t *testing.T) {
	first := newBackend("gpt", SlowStable(), 7)
	second := newBackend("gpt", SlowStable(), 7)
	req := request("req-1", 100)
	if first.Outcome(req, start) != second.Outcome(req, start) {
		t.Fatal("same id, seed, request, and time must give the same outcome for any caller")
	}
}

func TestDifferentBackendsDiffer(t *testing.T) {
	a := newBackend("model-a", SlowStable(), 7)
	b := newBackend("model-b", SlowStable(), 7)
	req := request("req-1", 100)
	if a.Outcome(req, start) == b.Outcome(req, start) {
		t.Fatal("different backends should not produce identical outcomes for the same request")
	}
}

func TestErrorRateDrivesErrored(t *testing.T) {
	never := newBackend("never", steady("never", Params{MeanLatencyMs: 100, ErrorRate: 0}), 1)
	always := newBackend("always", steady("always", Params{MeanLatencyMs: 100, ErrorRate: 1}), 1)
	for i := 0; i < 200; i++ {
		req := request(string(rune(i))+"-req", 50)
		if never.Outcome(req, start).Errored {
			t.Fatal("a backend with error rate 0 should never error")
		}
		if !always.Outcome(req, start).Errored {
			t.Fatal("a backend with error rate 1 should always error")
		}
	}
}

func TestCostFollowsTokensAndParams(t *testing.T) {
	b := newBackend("gpt", SlowStable(), 3)
	req := request("req-1", 120)
	outcome := b.Outcome(req, start)
	params := b.TrueParams(start)
	want := float64(req.Features.PromptTokens+outcome.OutputTokens) * params.CostPerToken
	if math.Abs(outcome.CostUSD-want) > 1e-12 {
		t.Fatalf("cost %v did not match tokens times cost per token %v", outcome.CostUSD, want)
	}
}

func TestMeanLatencyCentersOnTrueParams(t *testing.T) {
	b := newBackend("gpt", SlowStable(), 99)
	mean := b.TrueParams(start).MeanLatencyMs
	const samples = 5000
	total := 0.0
	for i := 0; i < samples; i++ {
		req := request("req-"+itoa(i), 100)
		total += b.Outcome(req, start).LatencyMs
	}
	average := total / samples
	if math.Abs(average-mean)/mean > 0.05 {
		t.Fatalf("average latency %v should sit within 5 percent of the true mean %v", average, mean)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
