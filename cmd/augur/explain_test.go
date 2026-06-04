package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunExplainUsesDryRunBackends(t *testing.T) {
	configPath := writeExplainConfig(t)
	var stdout bytes.Buffer

	err := runExplain(context.Background(), []string{
		"--config", configPath,
		"--prompt", "Say hello.",
		"--type", "chat",
		"--request-id", "req-chat",
	}, func(string) string {
		return ""
	}, &stdout)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	out := decodeExplainOutput(t, stdout.Bytes())
	if out.ProviderCalled {
		t.Fatal("explain must not call a provider")
	}
	if out.RouteName != "chat" || out.Selected != "fast" {
		t.Fatalf("unexpected route decision: %+v", out)
	}
	if len(out.FallbackPlan) != 1 || out.FallbackPlan[0] != "strong" {
		t.Fatalf("fallback plan got %v", out.FallbackPlan)
	}
}

func TestRunExplainReadsRequestFileMetadata(t *testing.T) {
	configPath := writeExplainConfig(t)
	requestPath := filepath.Join(t.TempDir(), "request.json")
	data := []byte(`{
		"messages": [{"role": "user", "content": "Implement a retry loop."}],
		"metadata": {
			"augur_request_type": "coding",
			"augur_prompt_tokens": "400",
			"augur_user_tier": "premium"
		}
	}`)
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var stdout bytes.Buffer

	err := run(context.Background(), []string{
		"simulate",
		"--config", configPath,
		"--request", requestPath,
		"--request-id", "req-code",
	}, func(string) string {
		return ""
	}, &stdout)
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}

	out := decodeExplainOutput(t, stdout.Bytes())
	if out.RequestType != "coding" {
		t.Fatalf("request type got %q", out.RequestType)
	}
	if out.PromptTokens != 400 {
		t.Fatalf("prompt tokens got %d", out.PromptTokens)
	}
	if out.RouteName != "coding" || out.Selected != "strong" {
		t.Fatalf("unexpected route decision: %+v", out)
	}
}

func TestRunExplainRequiresRequestShape(t *testing.T) {
	configPath := writeExplainConfig(t)
	err := runExplain(context.Background(), []string{"--config", configPath}, func(string) string {
		return ""
	}, bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("explain without request or prompt should fail")
	}
}

func writeExplainConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "augur.yaml")
	data := []byte(`
backends:
  - id: fast
    model: fast-model
    capabilities: ["chat"]
    max_completion_tokens: 64
  - id: strong
    model: strong-model
    capabilities: ["chat", "reasoning", "coding"]
    max_completion_tokens: 128
pricing:
  models:
    fast-model:
      input_cost_per_token: 0.000001
      output_cost_per_token: 0.000002
    strong-model:
      input_cost_per_token: 0.000004
      output_cost_per_token: 0.000008
routes:
  - name: chat
    match:
      task_types: ["chat"]
    candidates:
      - backend: fast
    fallbacks:
      - backend: strong
  - name: coding
    match:
      task_types: ["coding"]
    candidates:
      - backend: strong
  - name: reasoning
    match:
      task_types: ["reasoning"]
    candidates:
      - backend: strong
router:
  type: round_robin
budgets:
  cost_budget_usd: 0.02
  max_completion_tokens: 64
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func decodeExplainOutput(t *testing.T, data []byte) explainOutput {
	t.Helper()
	var out explainOutput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode explain output %q: %v", string(data), err)
	}
	return out
}
