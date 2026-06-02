package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appconfig "github.com/vanshamara/Augur/internal/config"
	"github.com/vanshamara/Augur/internal/core"
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
		if specs[i] != want[i] {
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
			Filters: []string{"health", "circuit", "concurrency"},
			Health:  map[core.BackendID]bool{"b": false},
		},
	}

	filters, err := buildFilters(config, []core.BackendID{"a", "b"})
	if err != nil {
		t.Fatalf("build filters: %v", err)
	}
	if len(filters) != 3 {
		t.Fatalf("filters got %d", len(filters))
	}
	candidates := filters[0].Apply(core.Request{ID: "req-1"}, []core.BackendID{"a", "b"})
	if len(candidates) != 1 || candidates[0] != "a" {
		t.Fatalf("health candidates got %v", candidates)
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
