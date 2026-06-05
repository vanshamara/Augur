package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/backend"
	appconfig "github.com/vanshamara/Augur/internal/config"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/httpapi"
	"github.com/vanshamara/Augur/internal/openaiapi"
	"github.com/vanshamara/Augur/internal/persist"
)

func TestParseBackends(t *testing.T) {
	specs, err := parseBackends("fast=gpt-fast, stable=gpt-stable, gpt-direct")
	if err != nil {
		t.Fatalf("parse backends: %v", err)
	}
	want := []appconfig.Backend{
		{ID: "fast", Model: "gpt-fast"},
		{ID: "stable", Model: "gpt-stable"},
		{ID: "gpt-direct", Model: "gpt-direct"},
	}
	if len(specs) != len(want) {
		t.Fatalf("got %d specs want %d", len(specs), len(want))
	}
	for i := range want {
		if specs[i].ID != want[i].ID || specs[i].Model != want[i].Model {
			t.Fatalf("spec %d got %+v want %+v", i, specs[i], want[i])
		}
	}
}

func TestParseBackendsRequiresValue(t *testing.T) {
	if _, err := parseBackends(""); err == nil {
		t.Fatal("empty backend list should fail")
	}
}

func TestReadConfigUsesDefaults(t *testing.T) {
	config, err := readConfig(func(key string) string {
		if key == "AUGUR_BACKENDS" {
			return "a=model-a"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if config.Server.Addr != appconfig.DefaultAddr {
		t.Fatalf("addr got %q", config.Server.Addr)
	}
	if config.Server.ReadTimeout.Duration != appconfig.DefaultReadTimeout {
		t.Fatalf("read timeout got %v", config.Server.ReadTimeout.Duration)
	}
	if config.Server.WriteTimeout.Duration != appconfig.DefaultWriteTimeout {
		t.Fatalf("write timeout got %v", config.Server.WriteTimeout.Duration)
	}
	if config.Server.IdleTimeout.Duration != appconfig.DefaultIdleTimeout {
		t.Fatalf("idle timeout got %v", config.Server.IdleTimeout.Duration)
	}
	if config.Server.ShutdownTimeout.Duration != appconfig.DefaultShutdownTimeout {
		t.Fatalf("shutdown timeout got %v", config.Server.ShutdownTimeout.Duration)
	}
	if config.Server.MaxBodyBytes != appconfig.DefaultMaxBodyBytes {
		t.Fatalf("max body bytes got %d", config.Server.MaxBodyBytes)
	}
	if config.Backends[0].ID != core.BackendID("a") || config.Backends[0].Model != "model-a" {
		t.Fatalf("backend got %+v", config.Backends[0])
	}
}

func TestReadConfigLoadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.json")
	if err := os.WriteFile(path, []byte(`{"server":{"addr":"127.0.0.1:9090"},"backends":[{"id":"a","model":"model-a"}]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := readConfig(func(key string) string {
		if key == "AUGUR_CONFIG" {
			return path
		}
		return ""
	})
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if config.Server.Addr != "127.0.0.1:9090" {
		t.Fatalf("addr got %q", config.Server.Addr)
	}
}

func TestRunValidateLoadsConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.yaml")
	if err := os.WriteFile(path, []byte("backends:\n  - id: a\n    model: model-a\nroutes:\n  - name: default\n    candidates:\n      - backend: a\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout bytes.Buffer

	err := runValidate([]string{"--config", path}, func(string) string {
		return ""
	}, &stdout)
	if err != nil {
		t.Fatalf("validate config: %v", err)
	}
	if !strings.Contains(stdout.String(), "config valid:") || !strings.Contains(stdout.String(), "1 backends, 1 routes") {
		t.Fatalf("validate output got %q", stdout.String())
	}
}

func TestRunValidateUsesEnvConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.json")
	if err := os.WriteFile(path, []byte(`{"backends":[{"id":"a","model":"model-a"}]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout bytes.Buffer

	err := runValidate(nil, func(key string) string {
		if key == "AUGUR_CONFIG" {
			return path
		}
		return ""
	}, &stdout)
	if err != nil {
		t.Fatalf("validate config: %v", err)
	}
	if !strings.Contains(stdout.String(), path) {
		t.Fatalf("validate output got %q", stdout.String())
	}
}

func TestRunValidateRejectsLearningWithoutBandit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.json")
	body := `{"backends":[{"id":"a","model":"model-a"}],"router":{"type":"round_robin"},"learning":{"enabled":true}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := runValidate([]string{"--config", path}, func(string) string {
		return ""
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "router.type to be bandit") {
		t.Fatalf("expected a learning-without-bandit error, got %v", err)
	}
}

func TestRunValidateRejectsHealthCheckWithoutHealthFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.json")
	body := `{"backends":[{"id":"a","model":"model-a"}],"data_plane":{"filters":["circuit"],"health_check":{"enabled":true,"interval":"5s","timeout":"2s"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := runValidate([]string{"--config", path}, func(string) string {
		return ""
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "requires the health filter") {
		t.Fatalf("expected a health-filter error, got %v", err)
	}
}

func TestRunValidateAcceptsLearningWithBandit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "augur.json")
	body := `{"backends":[{"id":"a","model":"model-a"}],"router":{"type":"bandit"},"learning":{"enabled":true}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout bytes.Buffer

	err := runValidate([]string{"--config", path}, func(string) string {
		return ""
	}, &stdout)
	if err != nil {
		t.Fatalf("validate learning with bandit: %v", err)
	}
	if !strings.Contains(stdout.String(), "config valid:") {
		t.Fatalf("validate output got %q", stdout.String())
	}
}

func TestRunValidateRequiresConfig(t *testing.T) {
	err := runValidate(nil, func(string) string {
		return ""
	}, io.Discard)
	if err == nil {
		t.Fatal("validate without config should fail")
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	err := run(context.Background(), []string{"unknown"}, func(string) string {
		return ""
	}, io.Discard)
	if err == nil {
		t.Fatal("unknown command should fail")
	}
}

func TestGatewayAuthKeysFromEnv(t *testing.T) {
	keys := gatewayAuthKeys(func(key string) string {
		if key == "AUGUR_GATEWAY_API_KEYS" {
			return " first-key, second-key ,, "
		}
		return ""
	})
	want := []string{"first-key", "second-key"}

	if len(keys) != len(want) {
		t.Fatalf("keys got %v want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("key %d got %q want %q", i, keys[i], want[i])
		}
	}
}

func TestGatewayAuthKeysDisabledByDefault(t *testing.T) {
	keys := gatewayAuthKeys(func(key string) string {
		return ""
	})

	if len(keys) != 0 {
		t.Fatalf("keys got %v", keys)
	}
}

func TestBuildRouterFromConfig(t *testing.T) {
	ids := []core.BackendID{"a", "b"}
	config := appconfig.App{
		Router: appconfig.Router{
			Type:  "cost_aware",
			Alpha: 0.2,
		},
		Backends: []appconfig.Backend{
			{ID: "a", Model: "model-a", InputCostPerToken: 2},
			{ID: "b", Model: "model-b", InputCostPerToken: 1},
		},
	}

	route, err := buildRouter(config, ids)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	if got := route.Router.Pick(context.Background(), core.Request{ID: "req-1"}, ids); got != "b" {
		t.Fatalf("router picked %s", got)
	}
}

func TestBuildRouterUsesPricingTable(t *testing.T) {
	config, err := appconfig.Parse([]byte(`{
		"backends": [
			{"id": "a", "model": "model-a"},
			{"id": "b", "model": "model-b"}
		],
		"pricing": {
			"models": {
				"model-a": {
					"input_cost_per_token": 0.000002,
					"output_cost_per_token": 0.000006
				},
				"model-b": {
					"input_cost_per_token": 0.000001,
					"output_cost_per_token": 0.000003
				}
			}
		},
		"router": {
			"type": "cost_aware"
		}
	}`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	ids := []core.BackendID{"a", "b"}
	route, err := buildRouter(config, ids)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	if got := route.Router.Pick(context.Background(), core.Request{ID: "req-1"}, ids); got != "b" {
		t.Fatalf("router picked %s", got)
	}
}

func TestBuildRouterCanBuildBandit(t *testing.T) {
	routing, err := buildRouter(appconfig.App{
		Router: appconfig.Router{Type: "bandit", Seed: 7},
		Backends: []appconfig.Backend{
			{ID: "a", Model: "model-a"},
		},
	}, []core.BackendID{"a"})
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	if routing.Bandit == nil || routing.Router.Name() != "bandit" {
		t.Fatalf("expected bandit routing, got %+v", routing)
	}
}

func TestBuildFiltersFromConfig(t *testing.T) {
	config := appconfig.App{
		DataPlane: appconfig.DataPlane{
			Filters: []string{"health", "circuit", "concurrency", "tenant"},
			Health:  map[core.BackendID]bool{"b": false},
		},
		Tenants: appconfig.Tenants{
			DefaultTenant: "default",
			Defaults: appconfig.Tenant{
				MaxInFlight: 1,
			},
		},
	}

	filters, err := buildFilters(config, []core.BackendID{"a", "b"})
	if err != nil {
		t.Fatalf("build filters: %v", err)
	}
	if len(filters) != 4 {
		t.Fatalf("filters got %d", len(filters))
	}
	candidates := filters[0].Apply(core.Request{ID: "req-1"}, []core.BackendID{"a", "b"})
	if len(candidates) != 1 || candidates[0] != "a" {
		t.Fatalf("health candidates got %v", candidates)
	}
	if filters[3].Name() != "tenant" {
		t.Fatalf("tenant filter got %s", filters[3].Name())
	}
}

func TestBuildRouteRulesFromConfig(t *testing.T) {
	routes := buildRouteRules([]appconfig.Route{
		{
			Name: "premium-reasoning",
			Match: appconfig.RouteMatch{
				TaskTypes: []core.RequestType{core.Reasoning},
				Tenants:   []string{"premium"},
				UserTiers: []string{"premium"},
			},
			Candidates: []appconfig.RouteCandidate{
				{Backend: "strong"},
				{Backend: "balanced"},
			},
			Fallbacks: []appconfig.RouteCandidate{
				{Backend: "safe"},
			},
			Canary: appconfig.RouteCanary{
				Backend:   "candidate",
				Percent:   5,
				StickyKey: "tenant_and_request",
				Shadow:    true,
			},
		},
	})

	if len(routes) != 1 {
		t.Fatalf("routes got %+v", routes)
	}
	route := routes[0]
	if route.Name != "premium-reasoning" || route.Match.TaskTypes[0] != core.Reasoning {
		t.Fatalf("route got %+v", route)
	}
	if len(route.Candidates) != 2 || route.Candidates[0] != "strong" || route.Candidates[1] != "balanced" {
		t.Fatalf("candidates got %+v", route.Candidates)
	}
	if len(route.Fallbacks) != 1 || route.Fallbacks[0] != "safe" {
		t.Fatalf("fallbacks got %+v", route.Fallbacks)
	}
	if route.Canary.Backend != "candidate" || route.Canary.Percent != 5 || !route.Canary.Shadow {
		t.Fatalf("canary got %+v", route.Canary)
	}
}

func TestBuildBackendCapabilitiesFromConfig(t *testing.T) {
	capabilities := buildBackendCapabilities([]appconfig.Backend{
		{
			ID:           "fast",
			Model:        "model-fast",
			Capabilities: []core.RequestType{core.Chat},
		},
		{
			ID:           "strong",
			Model:        "model-strong",
			Capabilities: []core.RequestType{core.Reasoning, core.Coding},
		},
	})

	if len(capabilities["fast"]) != 1 || capabilities["fast"][0] != core.Chat {
		t.Fatalf("fast capabilities got %+v", capabilities["fast"])
	}
	if len(capabilities["strong"]) != 2 || capabilities["strong"][1] != core.Coding {
		t.Fatalf("strong capabilities got %+v", capabilities["strong"])
	}
}

func TestBuildBackendTimeoutsFromConfig(t *testing.T) {
	timeouts := buildBackendTimeouts([]appconfig.Backend{
		{ID: "fast", Timeout: appconfig.Duration{Duration: 250 * time.Millisecond}},
		{ID: "strong"},
	})

	if timeouts["fast"] != 250*time.Millisecond {
		t.Fatalf("fast timeout got %v", timeouts["fast"])
	}
	if _, ok := timeouts["strong"]; ok {
		t.Fatalf("zero timeout should be omitted: %+v", timeouts)
	}
}

func TestStartActiveHealthChecksRequiresHealthFilter(t *testing.T) {
	stop, err := startActiveHealthChecks(context.Background(), appconfig.HealthCheck{Enabled: true}, nil, []backend.Backend{})
	if err == nil {
		stop()
		t.Fatal("active health should require health filter")
	}
}

func TestBuildCanaryConfigFromConfig(t *testing.T) {
	config := buildCanaryConfig(appconfig.Canary{
		P95RegressionRatio: 0.25,
		MaxErrorRate:       0.03,
		MinSamples:         4,
	})

	if config.Rollback.P95RegressionRatio != 0.25 || config.Rollback.MaxErrorRate != 0.03 || config.Rollback.MinSamples != 4 {
		t.Fatalf("canary config got %+v", config)
	}
}

func TestRequestDefaultsFromConfig(t *testing.T) {
	temperature := 0.3
	defaults := requestDefaults(appconfig.Budgets{
		LatencyBudgetMs:     900,
		CostBudgetUSD:       0.02,
		MaxCompletionTokens: 128,
		Temperature:         &temperature,
	})

	if defaults.LatencyBudgetMs != 900 || defaults.CostBudgetUSD != 0.02 || defaults.MaxCompletionTokens != 128 {
		t.Fatalf("defaults got %+v", defaults)
	}
	if defaults.Temperature == nil || *defaults.Temperature != 0.3 {
		t.Fatalf("temperature got %v", defaults.Temperature)
	}
}

func TestTenantRequestDefaultsFromConfig(t *testing.T) {
	temperature := 0.1
	defaults := tenantRequestDefaults(appconfig.App{
		Tenants: appconfig.Tenants{
			DefaultTenant: "default",
			Defaults: appconfig.Tenant{
				Policy: appconfig.TenantPolicy{
					UserTier: "standard",
				},
			},
			Overrides: map[string]appconfig.Tenant{
				"premium": {
					Policy: appconfig.TenantPolicy{
						LatencyBudgetMs:     800,
						CostBudgetUSD:       0.03,
						MaxCompletionTokens: 256,
						Temperature:         &temperature,
						UserTier:            "premium",
					},
				},
			},
		},
	})

	if defaults["default"].UserTier != "standard" {
		t.Fatalf("default tenant policy got %+v", defaults["default"])
	}
	premium := defaults["premium"]
	if premium.LatencyBudgetMs != 800 || premium.CostBudgetUSD != 0.03 || premium.MaxCompletionTokens != 256 {
		t.Fatalf("premium tenant defaults got %+v", premium)
	}
	if premium.Temperature == nil || *premium.Temperature != 0.1 || premium.UserTier != "premium" {
		t.Fatalf("premium tenant defaults got %+v", premium)
	}
}

func TestTenantLimitConfigFromConfig(t *testing.T) {
	config := tenantLimitConfig(appconfig.Tenants{
		DefaultTenant: "default",
		Defaults: appconfig.Tenant{
			MaxInFlight: 4,
			MaxCostUSD:  1.5,
		},
		Overrides: map[string]appconfig.Tenant{
			"premium": {
				MaxInFlight: 8,
				MaxCostUSD:  3.0,
			},
		},
	})

	if config.DefaultTenant != "default" || config.Defaults.MaxInFlight != 4 || config.Defaults.MaxCostUSD != 1.5 {
		t.Fatalf("tenant limit config got %+v", config)
	}
	if config.Tenants["premium"].MaxInFlight != 8 || config.Tenants["premium"].MaxCostUSD != 3.0 {
		t.Fatalf("tenant override got %+v", config.Tenants["premium"])
	}
}

func TestBuildSingleFlightScopesKeysByTenant(t *testing.T) {
	_, key := buildSingleFlight(appconfig.SingleFlight{
		Enabled: true,
		Key:     "prompt",
	})
	if key == nil {
		t.Fatal("single flight key should be set")
	}

	first := key(core.Request{TenantID: "tenant-a", Prompt: "same"})
	second := key(core.Request{TenantID: "tenant-b", Prompt: "same"})
	if first == second {
		t.Fatalf("tenant scoped keys matched: %q", first)
	}
}

func TestBuildHedgeIncludesTuning(t *testing.T) {
	budget := 0.25
	config := appconfig.Hedge{
		Enabled:           true,
		Delay:             appconfig.Duration{Duration: 75 * time.Millisecond},
		MaxInFlight:       4,
		BudgetFraction:    &budget,
		TriggerPercentile: 95,
		MaxExtraCalls:     2,
	}

	hedge := buildHedge(config)
	if !hedge.Enabled || hedge.Delay != 75*time.Millisecond || hedge.MaxInFlight != 4 {
		t.Fatalf("hedge got %+v", hedge)
	}
	if hedge.BudgetFraction == nil || *hedge.BudgetFraction != 0.25 {
		t.Fatalf("hedge budget got %+v", hedge)
	}
	if hedge.TriggerPercentile != 95 || hedge.MaxExtraCalls != 2 {
		t.Fatalf("hedge tuning got %+v", hedge)
	}
}

func TestBuildLiveGatewayRequiresBandit(t *testing.T) {
	_, err := buildLiveGateway(appconfig.App{
		Learning: appconfig.Learning{
			Enabled: true,
		},
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("live learning should require a bandit router")
	}
}

func TestBuildLiveGatewayLoadsPersistedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := persist.NewFileStore(persist.FileConfig{
		Path:     path,
		PolicyID: "default",
		Backends: []core.BackendID{"a"},
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Save(commandLearnedState("a", 4)); err != nil {
		t.Fatalf("save state: %v", err)
	}

	bandit := control.NewBanditRouter(control.BanditConfig{
		Policy:   control.NewPolicy(control.PolicyConfig{}),
		Backends: []core.BackendID{"a"},
	})
	gateway, err := buildLiveGateway(appconfig.App{
		Backends: []appconfig.Backend{
			{ID: "a", Model: "model-a"},
		},
		Learning: appconfig.Learning{
			Enabled: true,
			Persistence: appconfig.Persistence{
				Enabled: true,
				Path:    path,
			},
		},
	}, fakeCommandGateway{}, bandit, nil)
	if err != nil {
		t.Fatalf("build live gateway: %v", err)
	}
	defer closeGateway(gateway)

	state := bandit.LearnedState()
	if state.Reward.Arms["a"].Updates != 4 {
		t.Fatalf("restored updates got %v", state.Reward.Arms["a"].Updates)
	}
}

func TestHTTPGatewayRoutesCostAwareToCheapestOpenAIBackend(t *testing.T) {
	seen := newSeenModels()
	fakeOpenAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path got %q", r.URL.Path)
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}

		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		seen.add(body.Model)

		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"content": "selected " + body.Model,
					},
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     4,
				"completion_tokens": 2,
			},
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode provider response: %v", err)
			return
		}
	}))
	defer fakeOpenAI.Close()

	client, err := openaiapi.New(openaiapi.Config{
		BaseURL: fakeOpenAI.URL + "/v1",
		APIKey:  "test-key",
		Client:  fakeOpenAI.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	apiServer := commandHTTPTestServer(t, appconfig.App{
		Router: appconfig.Router{
			Type: "cost_aware",
		},
		Backends: []appconfig.Backend{
			{
				ID:    "expensive",
				Model: "model-expensive",
			},
			{
				ID:                 "cheap",
				Model:              "model-cheap",
				InputCostPerToken:  0.000001,
				OutputCostPerToken: 0.000002,
			},
		},
		Budgets: appconfig.Budgets{
			MaxCompletionTokens: 32,
			CostBudgetUSD:       0.01,
			RequirePricing:      true,
		},
	}, client)
	gatewayServer := httptest.NewServer(apiServer)
	defer gatewayServer.Close()

	body := `{"model":"augur-chat","messages":[{"role":"user","content":"Pick the best backend."}]}`
	req, err := http.NewRequest(http.MethodPost, gatewayServer.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := gatewayServer.Client().Do(req)
	if err != nil {
		t.Fatalf("post gateway request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status got %d body %s", resp.StatusCode, data)
	}
	if got := resp.Header.Get("X-Augur-Backend"); got != "cheap" {
		t.Fatalf("backend header got %q", got)
	}
	if seen.count("model-cheap") != 1 {
		t.Fatalf("cheap model calls got %d", seen.count("model-cheap"))
	}
	if seen.count("model-expensive") != 0 {
		t.Fatalf("expensive model calls got %d", seen.count("model-expensive"))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode gateway response: %v", err)
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Message.Content != "selected model-cheap" {
		t.Fatalf("response got %+v", decoded)
	}
}

func TestHTTPGatewayRoutesEmbeddingsToCapableBackend(t *testing.T) {
	var gotPath string
	var gotModel string
	fakeOpenAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":3}}`))
	}))
	defer fakeOpenAI.Close()

	client, err := openaiapi.New(openaiapi.Config{
		BaseURL: fakeOpenAI.URL + "/v1",
		APIKey:  "test-key",
		Client:  fakeOpenAI.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	apiServer := commandHTTPTestServer(t, appconfig.App{
		Router: appconfig.Router{Type: "round_robin"},
		Backends: []appconfig.Backend{
			{ID: "chat-only", Model: "model-chat", Capabilities: []core.RequestType{core.Chat}},
			{ID: "embedder", Model: "model-embed", Capabilities: []core.RequestType{core.Embedding}, InputCostPerToken: 0.000001},
		},
	}, client)
	gatewayServer := httptest.NewServer(apiServer)
	defer gatewayServer.Close()

	body := `{"model":"augur-embed","input":["embed this"]}`
	resp, err := gatewayServer.Client().Post(gatewayServer.URL+"/v1/embeddings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post embeddings: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status got %d body %s", resp.StatusCode, data)
	}
	if gotPath != "/v1/embeddings" {
		t.Fatalf("provider path got %q", gotPath)
	}
	if gotModel != "model-embed" {
		t.Fatalf("embedding routed to model %q, want the embedding-capable backend", gotModel)
	}
	if got := resp.Header.Get("X-Augur-Backend"); got != "embedder" {
		t.Fatalf("backend header got %q", got)
	}

	var decoded struct {
		Object string `json:"object"`
		Data   []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Object != "list" || len(decoded.Data) != 1 || len(decoded.Data[0].Embedding) != 2 {
		t.Fatalf("unexpected embeddings response %+v", decoded)
	}
}

func TestHTTPGatewayRoutesToAnthropicBackend(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var gotPath string
	anthropicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"content":[{"type":"text","text":"claude answer"}],"usage":{"input_tokens":4,"output_tokens":3}}`))
	}))
	defer anthropicServer.Close()

	openaiClient, err := openaiapi.New(openaiapi.Config{BaseURL: "http://unused.test/v1", APIKey: "unused"})
	if err != nil {
		t.Fatalf("new openai client: %v", err)
	}

	apiServer := commandHTTPTestServer(t, appconfig.App{
		Router: appconfig.Router{Type: "round_robin"},
		Backends: []appconfig.Backend{
			{ID: "claude", Model: "claude-3", Provider: "anthropic", BaseURL: anthropicServer.URL},
		},
	}, openaiClient)
	gatewayServer := httptest.NewServer(apiServer)
	defer gatewayServer.Close()

	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hi"}]}`
	resp, err := gatewayServer.Client().Post(gatewayServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status got %d body %s", resp.StatusCode, data)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("anthropic path got %q", gotPath)
	}
	if got := resp.Header.Get("X-Augur-Backend"); got != "claude" {
		t.Fatalf("backend header got %q", got)
	}
}

func TestHTTPGatewayRoutesToCustomBaseURLBackend(t *testing.T) {
	var gotAuth string
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"choices":[{"message":{"content":"local answer"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer localServer.Close()

	unused, err := openaiapi.New(openaiapi.Config{BaseURL: "http://unused.test/v1", APIKey: "unused"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	apiServer := commandHTTPTestServer(t, appconfig.App{
		Router: appconfig.Router{Type: "round_robin"},
		Backends: []appconfig.Backend{
			{ID: "local", Model: "llama3", BaseURL: localServer.URL + "/v1"},
		},
	}, unused)
	gatewayServer := httptest.NewServer(apiServer)
	defer gatewayServer.Close()

	body := `{"model":"augur-chat","messages":[{"role":"user","content":"hi"}]}`
	resp, err := gatewayServer.Client().Post(gatewayServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status got %d body %s", resp.StatusCode, data)
	}
	if got := resp.Header.Get("X-Augur-Backend"); got != "local" {
		t.Fatalf("backend header got %q", got)
	}
	if gotAuth != "" {
		t.Fatalf("keyless local backend should send no Authorization header, got %q", gotAuth)
	}
}

func TestCustomBaseURLBackendDoesNotInheritGlobalAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "global-key")
	config, err := appconfig.Parse([]byte(`
{
  "openai": {
    "api_key_env": "OPENAI_API_KEY"
  },
  "backends": [
    {
      "id": "local",
      "model": "llama3",
      "base_url": "http://localhost:11434/v1"
    }
  ]
}
`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	defaultClient, err := openaiapi.New(openaiapi.Config{APIKey: "default-key"})
	if err != nil {
		t.Fatalf("new default client: %v", err)
	}

	var gotAuth string
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"choices":[{"message":{"content":"local answer"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer localServer.Close()
	config.Backends[0].BaseURL = localServer.URL + "/v1"

	backends, err := buildBackends(config, defaultClient)
	if err != nil {
		t.Fatalf("build backends: %v", err)
	}
	_, err = backends[0].Call(context.Background(), core.Request{
		ID:     "req-local",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("call backend: %v", err)
	}

	if gotAuth != "" {
		t.Fatalf("local backend inherited Authorization header %q", gotAuth)
	}
}

type fakeCommandGateway struct{}

func (fakeCommandGateway) Call(ctx context.Context, req core.Request) (core.Response, error) {
	return core.Response{
		RequestID: req.ID,
		Backend:   "a",
		Outcome: core.Outcome{
			LatencyMs: 100,
		},
	}, nil
}

func commandLearnedState(id core.BackendID, updates float64) control.LearnedState {
	at := time.Unix(123, 0)
	arm := control.LinearArm{
		Precision: []float64{updates},
		Target:    []float64{updates},
		Last:      at,
		Updates:   updates,
	}
	snapshot := control.LinearSnapshot{
		Arms: map[core.BackendID]control.LinearArm{
			id: arm,
		},
	}
	return control.LearnedState{
		Reward:  snapshot,
		Quality: snapshot,
	}
}

type seenModels struct {
	mu     sync.Mutex
	counts map[string]int
}

func newSeenModels() *seenModels {
	return &seenModels{counts: map[string]int{}}
}

func (s *seenModels) add(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[model]++
}

func (s *seenModels) count(model string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[model]
}

func commandHTTPTestServer(t *testing.T, config appconfig.App, client *openaiapi.Client) http.Handler {
	t.Helper()

	backends, err := buildBackends(config, client)
	if err != nil {
		t.Fatalf("build backends: %v", err)
	}
	ids := backendIDs(config.Backends)
	routing, err := buildRouter(config, ids)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	filters, err := buildFilters(config, ids)
	if err != nil {
		t.Fatalf("build filters: %v", err)
	}
	singleFlight, singleFlightKey := buildSingleFlight(config.DataPlane.SingleFlight)

	gateway, err := dataplane.New(dataplane.Config{
		Router:          routing.Router,
		Backends:        backends,
		Routes:          buildRouteRules(config.Routes),
		Capabilities:    buildBackendCapabilities(config.Backends),
		Canary:          buildCanaryConfig(config.Canary),
		Pricing:         buildBackendPricing(config.Backends),
		RequirePricing:  config.Budgets.RequirePricing,
		Filters:         filters,
		Hedge:           buildHedge(config.DataPlane.Hedge),
		SingleFlight:    singleFlight,
		SingleFlightKey: singleFlightKey,
	})
	if err != nil {
		t.Fatalf("build gateway: %v", err)
	}
	servingGateway, err := buildLiveGateway(config, gateway, routing.Bandit, client)
	if err != nil {
		t.Fatalf("build live gateway: %v", err)
	}
	t.Cleanup(func() {
		closeGateway(servingGateway)
	})

	apiServer, err := httpapi.New(httpapi.Config{
		Gateway:       servingGateway,
		Defaults:      requestDefaults(config.Budgets),
		TenantHeader:  config.Tenants.Header,
		DefaultTenant: config.Tenants.DefaultTenant,
		Ready: func(ctx context.Context) bool {
			return len(backends) > 0
		},
	})
	if err != nil {
		t.Fatalf("new http api: %v", err)
	}
	return apiServer
}

func TestBuildHTTPServerUsesConfig(t *testing.T) {
	handler := http.NewServeMux()
	config := appconfig.Server{
		Addr: "127.0.0.1:9090",
		ReadTimeout: appconfig.Duration{
			Duration: 2 * time.Second,
		},
		WriteTimeout: appconfig.Duration{
			Duration: 3 * time.Second,
		},
		IdleTimeout: appconfig.Duration{
			Duration: 4 * time.Second,
		},
	}

	server := buildHTTPServer(config, handler)

	if server.Addr != "127.0.0.1:9090" {
		t.Fatalf("addr got %q", server.Addr)
	}
	if server.ReadTimeout != 2*time.Second {
		t.Fatalf("read timeout got %v", server.ReadTimeout)
	}
	if server.WriteTimeout != 3*time.Second {
		t.Fatalf("write timeout got %v", server.WriteTimeout)
	}
	if server.IdleTimeout != 4*time.Second {
		t.Fatalf("idle timeout got %v", server.IdleTimeout)
	}
}

func TestServeHTTPServerShutsDownGracefully(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	done := make(chan error, 1)

	go func() {
		done <- serveHTTPServer(ctx, server, listener, time.Second)
	}()

	resp, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status got %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}
