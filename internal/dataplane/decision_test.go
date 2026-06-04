package dataplane

import (
	"context"
	"errors"
	"testing"

	"github.com/vanshamara/Augur/internal/backend"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/router"
)

func TestDecisionLogRecordsSelectedBackend(t *testing.T) {
	chat := instantBackend("chat")
	reasoning := instantBackend("reasoning")
	log := NewDecisionLog(8)
	gateway, err := New(Config{
		Router:   router.NewStatic("reasoning"),
		Backends: []backend.Backend{chat, reasoning},
		Capabilities: map[core.BackendID][]core.RequestType{
			"chat":      {core.Chat},
			"reasoning": {core.Reasoning},
		},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"chat", "reasoning"},
			},
		},
		Decisions: log,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{
		ID:       "req-1",
		TenantID: "tenant-a",
		Features: core.Features{Type: core.Reasoning, PromptTokens: 12},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	record, ok := gateway.DecisionRecord("req-1")
	if !ok {
		t.Fatal("expected a decision record for req-1")
	}
	if record.Selected != "reasoning" {
		t.Fatalf("selected got %q", record.Selected)
	}
	if record.RouteName != "default" || record.RequestType != core.Reasoning || record.PromptTokens != 12 {
		t.Fatalf("record metadata got %+v", record)
	}
	if len(record.Candidates) != 2 {
		t.Fatalf("candidate set got %v", record.Candidates)
	}
	if !hasExclusion(record, "chat", "capability") {
		t.Fatalf("expected chat excluded at capability stage, got %+v", record.Excluded)
	}
}

func TestDecisionLogRecordsHealthExclusion(t *testing.T) {
	healthy := &fakeBackend{id: "healthy"}
	unhealthy := &fakeBackend{id: "unhealthy"}
	health := NewHealthFilter([]core.BackendID{"healthy", "unhealthy"})
	health.Set("unhealthy", false)
	log := NewDecisionLog(8)
	gateway, err := New(Config{
		Router:   router.NewStatic("healthy"),
		Backends: []backend.Backend{healthy, unhealthy},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"healthy", "unhealthy"},
			},
		},
		Filters:   []Filter{health},
		Decisions: log,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req-2"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	record, ok := gateway.DecisionRecord("req-2")
	if !ok {
		t.Fatal("expected a decision record for req-2")
	}
	if !hasExclusion(record, "unhealthy", "health") {
		t.Fatalf("expected unhealthy excluded at health stage, got %+v", record.Excluded)
	}
}

func TestDecisionLogRecordsBudgetExclusionAndError(t *testing.T) {
	expensive := &fakeBackend{id: "expensive"}
	log := NewDecisionLog(8)
	gateway, err := New(Config{
		Router:   router.NewStatic("expensive"),
		Backends: []backend.Backend{expensive},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"expensive"},
			},
		},
		Pricing: map[core.BackendID]BackendPrice{
			"expensive": {InputPerToken: 0.001, OutputPerToken: 0.001},
		},
		Decisions: log,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{
		ID:                  "req-3",
		MaxCompletionTokens: 100,
		Features:            core.Features{PromptTokens: 100, CostBudget: 0.0001},
	})
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("expected over-budget error, got %v", err)
	}

	record, ok := gateway.DecisionRecord("req-3")
	if !ok {
		t.Fatal("expected a decision record for req-3")
	}
	if !hasExclusion(record, "expensive", "budget") {
		t.Fatalf("expected expensive excluded at budget stage, got %+v", record.Excluded)
	}
	if record.Error == "" {
		t.Fatal("expected the record to capture the over-budget error")
	}
	if record.Selected != "" {
		t.Fatalf("no backend should be selected, got %q", record.Selected)
	}
}

func TestDecisionLogHashesCanaryStickyKey(t *testing.T) {
	stable := instantBackend("stable")
	candidate := instantBackend("candidate")
	log := NewDecisionLog(8)
	gateway, err := New(Config{
		Router:   router.NewStatic("stable"),
		Backends: []backend.Backend{stable, candidate},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"stable"},
				Canary: CanaryRule{
					Backend:   "candidate",
					Percent:   100,
					StickyKey: "tenant_id",
				},
			},
		},
		Decisions: log,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	_, err = gateway.Call(context.Background(), core.Request{ID: "req-4", TenantID: "tenant-secret"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	record, ok := gateway.DecisionRecord("req-4")
	if !ok {
		t.Fatal("expected a decision record for req-4")
	}
	if !record.Canary.Configured || !record.Canary.Assigned {
		t.Fatalf("canary record got %+v", record.Canary)
	}
	if record.Canary.StickyKeyHash == "" {
		t.Fatal("expected a sticky key hash")
	}
	if record.Canary.StickyKeyHash == "tenant-secret" {
		t.Fatal("sticky key hash must not be the raw tenant value")
	}
}

func TestDecisionLogDisabledByDefault(t *testing.T) {
	model := instantBackend("a")
	gateway, err := New(Config{
		Router:   router.NewStatic("a"),
		Backends: []backend.Backend{model},
		Routes: []RouteRule{
			{
				Name:       "default",
				Candidates: []core.BackendID{"a"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	if _, err := gateway.Call(context.Background(), core.Request{ID: "req-5"}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if records := gateway.DecisionRecords(); records != nil {
		t.Fatalf("expected no records when disabled, got %v", records)
	}
	if _, ok := gateway.DecisionRecord("req-5"); ok {
		t.Fatal("expected no lookup when the decision log is disabled")
	}
}

func TestDecisionLogEvictsOldestRecord(t *testing.T) {
	log := NewDecisionLog(2)
	log.put(&RouteDecisionRecord{RequestID: "a"})
	log.put(&RouteDecisionRecord{RequestID: "b"})
	log.put(&RouteDecisionRecord{RequestID: "c"})

	if _, ok := log.Lookup("a"); ok {
		t.Fatal("oldest record should have been evicted")
	}
	if _, ok := log.Lookup("b"); !ok {
		t.Fatal("record b should still be present")
	}
	if _, ok := log.Lookup("c"); !ok {
		t.Fatal("record c should still be present")
	}
}

func hasExclusion(record RouteDecisionRecord, backend core.BackendID, stage string) bool {
	for _, exclusion := range record.Excluded {
		if exclusion.Backend == backend && exclusion.Stage == stage {
			return true
		}
	}
	return false
}
