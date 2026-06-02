package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	appconfig "github.com/vanshamara/Augur/internal/config"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
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
