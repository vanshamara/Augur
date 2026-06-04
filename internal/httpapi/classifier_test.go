package httpapi

import (
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

func TestInferRequestOptionsClassifiesReasoning(t *testing.T) {
	body := chatCompletionRequest{
		Messages: []chatMessage{
			{Role: "user", Content: messageContent{Text: "Solve this carefully: if 8 workers finish 24 tasks in 6 hours, how many tasks can 12 workers finish in 9 hours?"}},
		},
	}

	options := inferRequestOptions(body, "", 0)
	if options.RequestType != core.Reasoning {
		t.Fatalf("request type got %q", options.RequestType)
	}
	if options.CostBudgetUSD < 0.05 || options.LatencyBudgetMs < 3000 {
		t.Fatalf("reasoning budgets got %+v", options)
	}
}

func TestInferRequestOptionsClassifiesCoding(t *testing.T) {
	body := chatCompletionRequest{
		Messages: []chatMessage{
			{Role: "user", Content: messageContent{Text: "Write a Go function that reverses a string and add a unit test."}},
		},
	}

	options := inferRequestOptions(body, "", 0)
	if options.RequestType != core.Coding {
		t.Fatalf("request type got %q", options.RequestType)
	}
	if options.CostBudgetUSD < 0.03 || options.LatencyBudgetMs < 2400 {
		t.Fatalf("coding budgets got %+v", options)
	}
}

func TestInferRequestOptionsKeepsSimplePromptsCheap(t *testing.T) {
	body := chatCompletionRequest{
		Messages: []chatMessage{
			{Role: "user", Content: messageContent{Text: "hello"}},
		},
	}

	options := inferRequestOptions(body, "", 0)
	if options.RequestType != core.Chat {
		t.Fatalf("request type got %q", options.RequestType)
	}
	if options.CostBudgetUSD > simpleCostBudgetUSD || options.LatencyBudgetMs != simpleLatencyBudgetMs {
		t.Fatalf("simple budgets got %+v", options)
	}
}

func TestInferRequestOptionsRespectsExplicitType(t *testing.T) {
	body := chatCompletionRequest{
		Messages: []chatMessage{
			{Role: "user", Content: messageContent{Text: "Write a Python function."}},
		},
	}

	options := inferRequestOptions(body, core.Chat, 0)
	if options.RequestType != core.Chat {
		t.Fatalf("request type got %q", options.RequestType)
	}
	if options.CostBudgetUSD != 0 {
		t.Fatalf("explicit chat should not inherit coding budget, got %+v", options)
	}
}

func TestInferRequestOptionsUsesPromptTokenHint(t *testing.T) {
	body := chatCompletionRequest{
		Messages: []chatMessage{
			{Role: "user", Content: messageContent{Text: "summarize this"}},
		},
	}

	options := inferRequestOptions(body, "", 900)
	if options.RequestType != core.Reasoning {
		t.Fatalf("request type got %q", options.RequestType)
	}
}

func TestInferRequestDefaultsMatchesRequestShape(t *testing.T) {
	reasoning := InferRequestDefaults("Solve this carefully.", "", 10)
	if reasoning.LatencyBudgetMs != reasoningLatencyBudgetMs || reasoning.CostBudgetUSD != reasoningCostBudgetUSD {
		t.Fatalf("reasoning defaults got %+v", reasoning)
	}

	simple := InferRequestDefaults("hi", core.Chat, 1)
	if simple.LatencyBudgetMs != simpleLatencyBudgetMs || simple.CostBudgetUSD != simpleCostBudgetUSD {
		t.Fatalf("simple defaults got %+v", simple)
	}
}
