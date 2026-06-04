package control

import (
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

func TestAttributionLogEvictsOldRecords(t *testing.T) {
	log := NewAttributionLogWithSize(2)
	log.RecordDecision(DecisionRecord{RequestID: "req-1", Backend: "a"})
	log.RecordResponse(ResponseRecord{
		RequestID: "req-1",
		Response:  core.Response{RequestID: "req-1", Backend: "a"},
		At:        time.Unix(1, 0),
	})
	log.RecordDecision(DecisionRecord{RequestID: "req-2", Backend: "b"})
	log.RecordDecision(DecisionRecord{RequestID: "req-3", Backend: "c"})

	if _, ok := log.Decision("req-1"); ok {
		t.Fatal("old decision should be evicted")
	}
	if _, ok := log.Response("req-1"); ok {
		t.Fatal("old response should be evicted with its decision")
	}
	if record, ok := log.Decision("req-3"); !ok || record.Backend != "c" {
		t.Fatalf("new decision got %+v ok=%v", record, ok)
	}
}

func TestAttributionLogCopiesStoredSlices(t *testing.T) {
	log := NewAttributionLogWithSize(2)
	features := []float64{1, 2}
	shadows := []core.BackendID{"shadow"}
	attempts := []core.BackendID{"a", "b"}

	log.RecordDecision(DecisionRecord{
		RequestID:      "req-1",
		Backend:        "a",
		Features:       features,
		ShadowBackends: shadows,
	})
	log.RecordResponse(ResponseRecord{
		RequestID: "req-1",
		Response: core.Response{
			RequestID:         "req-1",
			Backend:           "a",
			AttemptedBackends: attempts,
		},
	})
	features[0] = 99
	shadows[0] = "changed"
	attempts[0] = "changed"

	decision, ok := log.Decision("req-1")
	if !ok {
		t.Fatal("decision should exist")
	}
	response, ok := log.Response("req-1")
	if !ok {
		t.Fatal("response should exist")
	}
	if decision.Features[0] != 1 || decision.ShadowBackends[0] != "shadow" {
		t.Fatalf("decision slices were not copied: %+v", decision)
	}
	if response.Response.AttemptedBackends[0] != "a" {
		t.Fatalf("response slices were not copied: %+v", response.Response)
	}
}
