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
					"capabilities": ["chat", "reasoning"],
					"health_path": "/healthz",
					"timeout": "3s",
					"input_cost_per_token": 0.001,
					"output_cost_per_token": 0.002,
					"max_completion_tokens": 128
			}
		],
		"routes": [
			{
				"name": "reasoning-premium",
				"match": {
					"task_types": ["reasoning"],
					"tenants": ["premium"],
					"user_tiers": ["premium"]
				},
				"candidates": [
					{"backend": "fast"}
				],
				"fallbacks": [
					{"backend": "fast"}
				],
				"canary": {
					"backend": "fast",
					"percent": 5,
					"sticky_key": "tenant_and_request",
					"shadow": true
				}
			}
		],
		"router": {"type": "p2c", "seed": 9, "alpha": 0.3, "p2c_window": 32},
		"data_plane": {
			"filters": ["health", "circuit", "concurrency"],
			"health": {"fast": true},
			"circuit": {"failure_threshold": 3, "recovery_after": "2s", "half_open_max": 2},
			"concurrency": {"initial_limit": 8, "min_limit": 2, "max_limit": 16, "target_latency_ms": 900},
			"hedge": {
				"enabled": true,
				"delay": "75ms",
				"max_in_flight": 4,
				"budget_fraction": 0.25,
				"trigger_percentile": 95,
				"max_extra_calls": 2
				},
				"single_flight": {"enabled": true, "key": "prompt"},
				"health_check": {
					"enabled": true,
					"interval": "2s",
					"timeout": "500ms",
					"failure_threshold": 2,
					"success_threshold": 2
				}
			},
		"learning": {
			"enabled": true,
			"tau": "10m",
			"prior_precision": 2,
			"queue_size": 256,
			"persistence": {"enabled": true, "path": ".augur/state.json", "save_every": 4},
			"judge": {"enabled": true, "model": "judge-model", "seed": 11}
		},
			"canary": {
				"p95_regression_ratio": 0.25,
				"max_error_rate": 0.03,
				"min_samples": 40
			},
		"tenants": {
			"header": "X-Augur-Tenant",
			"default_tenant": "default",
			"defaults": {
				"max_in_flight": 8,
				"max_cost_usd": 10.0,
				"policy": {
					"user_tier": "standard"
				}
			},
			"overrides": {
				"premium": {
					"max_in_flight": 16,
					"max_cost_usd": 25.0,
					"policy": {
						"latency_budget_ms": 900,
						"cost_budget_usd": 0.02,
						"max_completion_tokens": 512,
						"temperature": 0.1,
						"user_tier": "premium"
					}
				}
			}
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
	if len(config.Backends[0].Capabilities) != 2 || config.Backends[0].Capabilities[1] != "reasoning" {
		t.Fatalf("backend capabilities got %+v", config.Backends[0].Capabilities)
	}
	if config.Backends[0].HealthPath != "/healthz" || config.Backends[0].Timeout.Duration.String() != "3s" {
		t.Fatalf("backend health config got %+v", config.Backends[0])
	}
	if config.Backends[0].HealthPath != "/healthz" || config.Backends[0].Timeout.Duration.String() != "3s" {
		t.Fatalf("backend health config got %+v", config.Backends[0])
	}
	if len(config.Routes) != 1 || config.Routes[0].Name != "reasoning-premium" {
		t.Fatalf("routes got %+v", config.Routes)
	}
	if config.Routes[0].Match.TaskTypes[0] != "reasoning" || config.Routes[0].Candidates[0].Backend != "fast" {
		t.Fatalf("route details got %+v", config.Routes[0])
	}
	if len(config.Routes[0].Fallbacks) != 1 || config.Routes[0].Fallbacks[0].Backend != "fast" {
		t.Fatalf("route fallbacks got %+v", config.Routes[0].Fallbacks)
	}
	if config.Routes[0].Canary.Backend != "fast" || config.Routes[0].Canary.Percent != 5 || !config.Routes[0].Canary.Shadow {
		t.Fatalf("route canary got %+v", config.Routes[0].Canary)
	}
	if config.Router.Type != "p2c" || config.Router.P2CWindow != 32 {
		t.Fatalf("router got %+v", config.Router)
	}
	if config.DataPlane.Circuit.RecoveryAfter.Duration.String() != "2s" {
		t.Fatalf("recovery duration got %v", config.DataPlane.Circuit.RecoveryAfter.Duration)
	}
	if config.DataPlane.Hedge.BudgetFraction == nil || *config.DataPlane.Hedge.BudgetFraction != 0.25 {
		t.Fatalf("hedge budget got %+v", config.DataPlane.Hedge)
	}
	if !config.DataPlane.HealthCheck.Enabled || config.DataPlane.HealthCheck.Timeout.Duration.String() != "500ms" {
		t.Fatalf("health check got %+v", config.DataPlane.HealthCheck)
	}
	if config.DataPlane.Hedge.TriggerPercentile != 95 || config.DataPlane.Hedge.MaxExtraCalls != 2 {
		t.Fatalf("hedge tuning got %+v", config.DataPlane.Hedge)
	}
	if !config.DataPlane.HealthCheck.Enabled || config.DataPlane.HealthCheck.Interval.Duration.String() != "2s" {
		t.Fatalf("health check got %+v", config.DataPlane.HealthCheck)
	}
	if config.DataPlane.HealthCheck.Timeout.Duration.String() != "500ms" || config.DataPlane.HealthCheck.FailureThreshold != 2 {
		t.Fatalf("health check tuning got %+v", config.DataPlane.HealthCheck)
	}
	if config.Canary.P95RegressionRatio != 0.25 || config.Canary.MaxErrorRate != 0.03 || config.Canary.MinSamples != 40 {
		t.Fatalf("canary got %+v", config.Canary)
	}
	if config.Tenants.Header != "X-Augur-Tenant" || config.Tenants.DefaultTenant != "default" {
		t.Fatalf("tenant identity got %+v", config.Tenants)
	}
	if config.Tenants.Defaults.MaxInFlight != 8 || config.Tenants.Defaults.MaxCostUSD != 10.0 {
		t.Fatalf("tenant defaults got %+v", config.Tenants.Defaults)
	}
	if config.Tenants.Overrides["premium"].Policy.UserTier != "premium" {
		t.Fatalf("tenant override got %+v", config.Tenants.Overrides["premium"])
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
	if config.Tenants.Header != "X-Augur-Tenant" || config.Tenants.DefaultTenant != "default" {
		t.Fatalf("tenant defaults got %+v", config.Tenants)
	}
	if config.Backends[0].ID != "model-a" {
		t.Fatalf("backend id got %q", config.Backends[0].ID)
	}
}

func TestParseAppliesPricingTableByModel(t *testing.T) {
	config, err := Parse([]byte(`{
		"backends": [
			{"id": "fast", "model": "model-fast"}
		],
		"pricing": {
			"models": {
				"model-fast": {
					"input_cost_per_token": 0.000001,
					"output_cost_per_token": 0.000004
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	backend := config.Backends[0]
	if backend.InputCostPerToken != 0.000001 || backend.OutputCostPerToken != 0.000004 {
		t.Fatalf("backend prices got %+v", backend)
	}
}

func TestParseKeepsBackendPricingOverrides(t *testing.T) {
	config, err := Parse([]byte(`{
		"backends": [
			{
				"id": "fast",
				"model": "model-fast",
				"input_cost_per_token": 0.000010,
				"output_cost_per_token": 0.000020
			}
		],
		"pricing": {
			"models": {
				"model-fast": {
					"input_cost_per_token": 0.000001,
					"output_cost_per_token": 0.000004
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	backend := config.Backends[0]
	if backend.InputCostPerToken != 0.000010 || backend.OutputCostPerToken != 0.000020 {
		t.Fatalf("backend prices got %+v", backend)
	}
}

func TestParseLeavesUnknownModelPricesAtZero(t *testing.T) {
	config, err := Parse([]byte(`{
		"backends": [
			{"id": "fast", "model": "missing-model"}
		],
		"pricing": {
			"models": {
				"known-model": {
					"input_cost_per_token": 0.000001,
					"output_cost_per_token": 0.000004
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	backend := config.Backends[0]
	if backend.InputCostPerToken != 0 || backend.OutputCostPerToken != 0 {
		t.Fatalf("backend prices got %+v", backend)
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
    capabilities: ["chat", "reasoning"]
    health_path: "/healthz"
    timeout: "3s"
    input_cost_per_token: 0.001
    output_cost_per_token: 0.002
    max_completion_tokens: 128
routes:
  - name: "default"
    candidates:
      - backend: "fast"
    fallbacks:
      - backend: "fast"
    canary:
      backend: "fast"
      percent: 5
      sticky_key: "tenant_and_request"
      shadow: true
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
    budget_fraction: 0.25
    trigger_percentile: 95
    max_extra_calls: 2
  single_flight:
    enabled: true
    key: "prompt"
  health_check:
    enabled: true
    interval: "2s"
    timeout: "500ms"
    failure_threshold: 2
    success_threshold: 2
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
canary:
  p95_regression_ratio: 0.25
  max_error_rate: 0.03
  min_samples: 40
tenants:
  header: "X-Augur-Tenant"
  default_tenant: "default"
  defaults:
    max_in_flight: 8
    max_cost_usd: 10.0
    policy:
      user_tier: "standard"
  overrides:
    premium:
      max_in_flight: 16
      max_cost_usd: 25.0
      policy:
        latency_budget_ms: 900
        cost_budget_usd: 0.02
        max_completion_tokens: 512
        temperature: 0.1
        user_tier: "premium"
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
	if len(config.Backends[0].Capabilities) != 2 || config.Backends[0].Capabilities[1] != "reasoning" {
		t.Fatalf("backend capabilities got %+v", config.Backends[0].Capabilities)
	}
	if len(config.Routes) != 1 || config.Routes[0].Name != "default" {
		t.Fatalf("routes got %+v", config.Routes)
	}
	if len(config.Routes[0].Fallbacks) != 1 || config.Routes[0].Fallbacks[0].Backend != "fast" {
		t.Fatalf("route fallbacks got %+v", config.Routes[0].Fallbacks)
	}
	if config.Routes[0].Canary.Backend != "fast" || config.Routes[0].Canary.Percent != 5 || !config.Routes[0].Canary.Shadow {
		t.Fatalf("route canary got %+v", config.Routes[0].Canary)
	}
	if config.Router.Type != "p2c" || config.Router.P2CWindow != 32 {
		t.Fatalf("router got %+v", config.Router)
	}
	if config.Policy.Objective.Type != control.BlendObjective || config.Policy.Constraints.QualityGate != control.GateOnLCB {
		t.Fatalf("policy got %+v", config.Policy)
	}
	if config.DataPlane.Hedge.BudgetFraction == nil || *config.DataPlane.Hedge.BudgetFraction != 0.25 {
		t.Fatalf("hedge budget got %+v", config.DataPlane.Hedge)
	}
	if config.Canary.P95RegressionRatio != 0.25 || config.Canary.MinSamples != 40 {
		t.Fatalf("canary got %+v", config.Canary)
	}
	if config.Tenants.Overrides["premium"].MaxInFlight != 16 {
		t.Fatalf("tenant override got %+v", config.Tenants.Overrides["premium"])
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

func TestParseRejectsBadBackendCapability(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [
			{"id": "a", "model": "model-a", "capabilities": ["image"]}
		]
	}`))
	if err == nil {
		t.Fatal("bad backend capability should fail")
	}
}

func TestParseRejectsBadSingleFlightKey(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"data_plane":{"single_flight":{"enabled":true,"key":"bad"}}}`))
	if err == nil {
		t.Fatal("bad single flight key should fail")
	}
}

func TestParseRejectsDuplicateRouteNames(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [
			{"name": "same", "candidates": [{"backend": "a"}]},
			{"name": "same", "candidates": [{"backend": "a"}]}
		]
	}`))
	if err == nil {
		t.Fatal("duplicate route names should fail")
	}
}

func TestParseRejectsRouteWithoutCandidates(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{"name": "empty"}]
	}`))
	if err == nil {
		t.Fatal("route without candidates should fail")
	}
}

func TestParseRejectsRouteWithUnknownBackend(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{"name": "bad", "candidates": [{"backend": "missing"}]}]
	}`))
	if err == nil {
		t.Fatal("route with unknown backend should fail")
	}
}

func TestParseRejectsRouteWithUnknownFallbackBackend(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{
			"name": "bad",
			"candidates": [{"backend": "a"}],
			"fallbacks": [{"backend": "missing"}]
		}]
	}`))
	if err == nil {
		t.Fatal("route with unknown fallback backend should fail")
	}
}

func TestParseRejectsRouteWithUnknownCanaryBackend(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{
			"name": "bad",
			"candidates": [{"backend": "a"}],
			"canary": {"backend": "missing", "percent": 5}
		}]
	}`))
	if err == nil {
		t.Fatal("route with unknown canary backend should fail")
	}
}

func TestParseRejectsRouteWithBadCanaryPercent(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{
			"name": "bad",
			"candidates": [{"backend": "a"}],
			"canary": {"backend": "a", "percent": 101}
		}]
	}`))
	if err == nil {
		t.Fatal("route with bad canary percent should fail")
	}
}

func TestParseRejectsRouteWithBadCanaryStickyKey(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{
			"name": "bad",
			"candidates": [{"backend": "a"}],
			"canary": {"backend": "a", "percent": 5, "sticky_key": "bad"}
		}]
	}`))
	if err == nil {
		t.Fatal("route with bad canary sticky key should fail")
	}
}

func TestParseRejectsRouteWithBadTaskType(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [{
			"name": "bad-task",
			"match": {"task_types": ["image"]},
			"candidates": [{"backend": "a"}]
		}]
	}`))
	if err == nil {
		t.Fatal("route with bad task type should fail")
	}
}

func TestParseRejectsMultipleDefaultRoutes(t *testing.T) {
	_, err := Parse([]byte(`{
		"backends": [{"id": "a", "model": "model-a"}],
		"routes": [
			{"name": "default-a", "candidates": [{"backend": "a"}]},
			{"name": "default-b", "candidates": [{"backend": "a"}]}
		]
	}`))
	if err == nil {
		t.Fatal("multiple default routes should fail")
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

func TestParseRejectsNegativeBackendTimeout(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a","timeout":"-1s"}]}`))
	if err == nil {
		t.Fatal("negative backend timeout should fail")
	}
}

func TestParseRejectsNegativeActiveHealthInterval(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"data_plane":{"health_check":{"interval":"-1s"}}}`))
	if err == nil {
		t.Fatal("negative health check interval should fail")
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

func TestParseRejectsInvalidHedgeBudget(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"data_plane":{"hedge":{"budget_fraction":1.5}}}`))
	if err == nil {
		t.Fatal("bad hedge budget should fail")
	}
}

func TestParseRejectsInvalidHedgeTriggerPercentile(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"data_plane":{"hedge":{"trigger_percentile":101}}}`))
	if err == nil {
		t.Fatal("bad hedge trigger percentile should fail")
	}
}

func TestParseRejectsNegativeTenantLimit(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"tenants":{"defaults":{"max_in_flight":-1}}}`))
	if err == nil {
		t.Fatal("negative tenant limit should fail")
	}
}

func TestParseRejectsEmptyTenantOverride(t *testing.T) {
	_, err := Parse([]byte(`{"backends":[{"model":"model-a"}],"tenants":{"overrides":{"":{"max_in_flight":1}}}}`))
	if err == nil {
		t.Fatal("empty tenant override should fail")
	}
}

func TestCanaryBuildsRollbackConfig(t *testing.T) {
	canary := Canary{
		P95RegressionRatio: 0.30,
		MaxErrorRate:       0.04,
		MinSamples:         50,
	}

	rollback := canary.RollbackConfig()
	if rollback.P95RegressionRatio != 0.30 || rollback.MaxErrorRate != 0.04 {
		t.Fatalf("rollback config got %+v", rollback)
	}
	if rollback.MinSamples != 50 {
		t.Fatalf("rollback config got %+v", rollback)
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
