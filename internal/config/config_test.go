package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vanshamara/Augur/internal/control"
)

func TestParseLoadsGatewayConfig(t *testing.T) {
	data := []byte(`{
		"server": {
			"addr": "127.0.0.1:9090",
			"max_body_bytes": 65536,
			"read_timeout": "4s",
			"write_timeout": "20s",
			"idle_timeout": "1m",
			"shutdown_timeout": "8s"
		},
		"openai": {"base_url": "http://example.test/v1", "api_key_env": "AUGUR_KEY"},
		"backends": [
			{
				"id": "fast",
				"model": "model-fast",
				"input_cost_per_token": 0.001,
				"output_cost_per_token": 0.002,
				"max_completion_tokens": 128
			}
		],
		"router": {"type": "p2c", "seed": 9, "alpha": 0.3, "p2c_window": 32},
		"data_plane": {
			"filters": ["health", "circuit", "concurrency"],
			"health": {"fast": true},
			"circuit": {"failure_threshold": 3, "recovery_after": "2s", "half_open_max": 2},
			"concurrency": {"initial_limit": 8, "min_limit": 2, "max_limit": 16, "target_latency_ms": 900},
			"hedge": {"enabled": true, "delay": "75ms", "max_in_flight": 4},
			"single_flight": {"enabled": true, "key": "prompt"}
		},
		"learning": {
			"enabled": true,
			"tau": "10m",
			"prior_precision": 2,
			"queue_size": 256,
			"persistence": {"enabled": true, "path": ".augur/state.json", "save_every": 4},
			"judge": {"enabled": true, "model": "judge-model", "seed": 11}
		},
		"policy": {
			"id": "prod",
			"constraints": {"max_p95_ms": 1200, "min_quality": 0.85, "max_error_rate": 0.02, "quality_gate": "lcb"},
			"objective": {"type": "blend", "latency_weight": 1.2, "cost_weight": 0.8},
			"exploration": {"cold_start_budget": 0.03, "judge_sample_rate": 0.1, "uncertainty_sampling": true},
			"on_infeasible": "fail_closed"
		},
		"budgets": {"latency_budget_ms": 1200, "cost_budget_usd": 0.01, "max_completion_tokens": 256, "temperature": 0.2}
	}`)

	config, err := Parse(data)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if config.Server.Addr != "127.0.0.1:9090" || config.OpenAI.APIKeyEnv != "AUGUR_KEY" {
		t.Fatalf("unexpected server/openai config %+v %+v", config.Server, config.OpenAI)
	}
	if config.Server.MaxBodyBytes != 65536 {
		t.Fatalf("max body bytes got %d", config.Server.MaxBodyBytes)
	}
	if config.Server.ReadTimeout.Duration.String() != "4s" || config.Server.WriteTimeout.Duration.String() != "20s" {
		t.Fatalf("server timeouts got %+v", config.Server)
	}
	if config.Server.IdleTimeout.Duration.String() != "1m0s" || config.Server.ShutdownTimeout.Duration.String() != "8s" {
		t.Fatalf("server timeouts got %+v", config.Server)
	}
	if config.Backends[0].ID != "fast" || config.Backends[0].Model != "model-fast" {
		t.Fatalf("backend got %+v", config.Backends[0])
	}
	if config.Router.Type != "p2c" || config.Router.P2CWindow != 32 {
		t.Fatalf("router got %+v", config.Router)
	}
	if config.DataPlane.Circuit.RecoveryAfter.Duration.String() != "2s" {
		t.Fatalf("recovery duration got %v", config.DataPlane.Circuit.RecoveryAfter.Duration)
	}
	if config.Policy.Objective.Type != control.BlendObjective || config.Policy.Constraints.QualityGate != control.GateOnLCB {
		t.Fatalf("policy got %+v", config.Policy)
	}
	if !config.Learning.Enabled || config.Learning.Judge.Model != "judge-model" || config.Learning.QueueSize != 256 {
		t.Fatalf("learning got %+v", config.Learning)
	}
	if !config.Learning.Persistence.Enabled || config.Learning.Persistence.Path != ".augur/state.json" || config.Learning.Persistence.SaveEvery != 4 {
		t.Fatalf("persistence got %+v", config.Learning.Persistence)
	}
	if config.Budgets.Temperature == nil || *config.Budgets.Temperature != 0.2 {
		t.Fatalf("temperature got %v", config.Budgets.Temperature)
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	config, err := Parse([]byte(`{"backends":[{"model":"model-a"}]}`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if config.Server.Addr != DefaultAddr {
		t.Fatalf("addr got %q", config.Server.Addr)
	}
	if config.Server.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Fatalf("max body bytes got %d", config.Server.MaxBodyBytes)
	}
	if config.Server.ReadTimeout.Duration != DefaultReadTimeout {
		t.Fatalf("read timeout got %v", config.Server.ReadTimeout.Duration)
	}
	if config.Server.WriteTimeout.Duration != DefaultWriteTimeout {
		t.Fatalf("write timeout got %v", config.Server.WriteTimeout.Duration)
	}
	if config.Server.IdleTimeout.Duration != DefaultIdleTimeout {
		t.Fatalf("idle timeout got %v", config.Server.IdleTimeout.Duration)
	}
	if config.Server.ShutdownTimeout.Duration != DefaultShutdownTimeout {
		t.Fatalf("shutdown timeout got %v", config.Server.ShutdownTimeout.Duration)
	}
	if config.OpenAI.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("api key env got %q", config.OpenAI.APIKeyEnv)
	}
	if config.Router.Type != "round_robin" {
		t.Fatalf("router got %q", config.Router.Type)
	}
	if config.Learning.Persistence.SaveEvery != 1 {
		t.Fatalf("persistence save every got %d", config.Learning.Persistence.SaveEvery)
	}
	if config.Backends[0].ID != "model-a" {
		t.Fatalf("backend id got %q", config.Backends[0].ID)
	}
}

func TestParseYAMLLoadsGatewayConfig(t *testing.T) {
	data := []byte(`
server:
  addr: "127.0.0.1:9090"
  max_body_bytes: 65536
  read_timeout: "4s"
  write_timeout: "20s"
  idle_timeout: "1m"
  shutdown_timeout: "8s"
openai:
  base_url: "http://example.test/v1"
  api_key_env: "AUGUR_KEY"
backends:
  - id: "fast"
    model: "model-fast"
    input_cost_per_token: 0.001
    output_cost_per_token: 0.002
    max_completion_tokens: 128
router:
  type: "p2c"
  seed: 9
  alpha: 0.3
  p2c_window: 32
data_plane:
  filters: ["health", "circuit", "concurrency"]
  health:
    fast: true
  circuit:
    failure_threshold: 3
    recovery_after: "2s"
    half_open_max: 2
  concurrency:
    initial_limit: 8
    min_limit: 2
    max_limit: 16
    target_latency_ms: 900
  hedge:
    enabled: true
    delay: "75ms"
    max_in_flight: 4
  single_flight:
    enabled: true
    key: "prompt"
learning:
  enabled: true
  tau: "10m"
  prior_precision: 2
  queue_size: 256
  persistence:
    enabled: true
    path: ".augur/state.json"
    save_every: 4
  judge:
    enabled: true
    model: "judge-model"
    seed: 11
policy:
  id: "prod"
  constraints:
    max_p95_ms: 1200
    min_quality: 0.85
    max_error_rate: 0.02
    quality_gate: "lcb"
  objective:
    type: "blend"
    latency_weight: 1.2
    cost_weight: 0.8
  exploration:
    cold_start_budget: 0.03
    judge_sample_rate: 0.1
    uncertainty_sampling: true
  on_infeasible: "fail_closed"
budgets:
  latency_budget_ms: 1200
  cost_budget_usd: 0.01
  max_completion_tokens: 256
  temperature: 0.2
`)

	config, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("parse yaml config: %v", err)
	}

	if config.Server.Addr != "127.0.0.1:9090" || config.OpenAI.APIKeyEnv != "AUGUR_KEY" {
		t.Fatalf("unexpected server/openai config %+v %+v", config.Server, config.OpenAI)
	}
	if config.Backends[0].ID != "fast" || config.Backends[0].Model != "model-fast" {
		t.Fatalf("backend got %+v", config.Backends[0])
	}
	if config.Router.Type != "p2c" || config.Router.P2CWindow != 32 {
		t.Fatalf("router got %+v", config.Router)
	}
	if config.Policy.Objective.Type != control.BlendObjective || config.Policy.Constraints.QualityGate != control.GateOnLCB {
		t.Fatalf("policy got %+v", config.Policy)
	}
	if !config.Learning.Persistence.Enabled || config.Learning.Persistence.SaveEvery != 4 {
		t.Fatalf("persistence got %+v", config.Learning.Persistence)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte(`{"unknown": true, "backends":[{"model":"model-a"}]}`))
	if err == nil {
		t.Fatal("unknown fields should fail")
	}
}

func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	_, err := ParseYAML([]byte("unknown: true\nbackends:\n  - model: model-a\n"))
	if err == nil {
		t.Fatal("unknown yaml fields should fail")
	}
}

func TestParseRejectsBadRouter(t *testing.T) {
	_, err := Parse([]byte(`{"router":{"type":"bad"},"backends":[{"model":"model-a"}]}`))
	if err == nil {
		t.Fatal("bad router should fail")
	}
}

func TestParseRejectsBadSingleFlightKey(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"data_plane":{"single_flight":{"enabled":true,"key":"bad"}}}`))
	if err == nil {
		t.Fatal("bad single flight key should fail")
	}
}

func TestParseRejectsNegativeServerLimits(t *testing.T) {
	_, err := Parse([]byte(`{"server":{"max_body_bytes":-1},"backends":[{"model":"model-a"}]}`))
	if err == nil {
		t.Fatal("negative max body bytes should fail")
	}
}

func TestParseRejectsNegativeServerTimeouts(t *testing.T) {
	_, err := Parse([]byte(`{"server":{"read_timeout":"-1s"},"backends":[{"model":"model-a"}]}`))
	if err == nil {
		t.Fatal("negative read timeout should fail")
	}
}

func TestParseRejectsMissingJudgeModel(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"learning":{"judge":{"enabled":true}}}`))
	if err == nil {
		t.Fatal("enabled judge should require a model")
	}
}

func TestParseRejectsEnabledJudgeWithoutSampleRate(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"learning":{"judge":{"enabled":true,"model":"judge-model"}}}`))
	if err == nil {
		t.Fatal("enabled judge should require a positive sample rate")
	}
}

func TestParseRejectsEnabledPersistenceWithoutLearning(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"learning":{"persistence":{"enabled":true,"path":".augur/state.json"}}}`))
	if err == nil {
		t.Fatal("enabled persistence should require learning")
	}
}

func TestParseRejectsEnabledPersistenceWithoutPath(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"learning":{"enabled":true,"persistence":{"enabled":true}}}`))
	if err == nil {
		t.Fatal("enabled persistence should require a path")
	}
}

func TestParseRejectsNegativePersistenceSaveEvery(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"learning":{"enabled":true,"persistence":{"enabled":true,"path":".augur/state.json","save_every":-1}}}`))
	if err == nil {
		t.Fatal("negative persistence save_every should fail")
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.json")
	if err := os.WriteFile(path, []byte(`{"backends":[{"model":"model-a"}]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if config.Backends[0].Model != "model-a" {
		t.Fatalf("backend got %+v", config.Backends[0])
	}
}

func TestLoadFileParsesYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.yaml")
	if err := os.WriteFile(path, []byte("backends:\n  - model: model-a\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if config.Backends[0].Model != "model-a" {
		t.Fatalf("backend got %+v", config.Backends[0])
	}
}

func TestExampleConfigsLoad(t *testing.T) {
	paths := exampleConfigPaths(t)
	if len(paths) == 0 {
		t.Fatal("expected at least one example config")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			config, err := LoadFile(path)
			if err != nil {
				t.Fatalf("load example config: %v", err)
			}
			if len(config.Backends) == 0 {
				t.Fatal("example config should define at least one backend")
			}
		})
	}
}

func exampleConfigPaths(t *testing.T) []string {
	t.Helper()
	patterns := []string{
		"../../configs/*.example.json",
		"../../configs/*.example.yaml",
		"../../configs/*.example.yml",
	}
	var paths []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("find example configs: %v", err)
		}
		paths = append(paths, matches...)
	}
	return paths
}
