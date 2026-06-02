package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/vanshamara/Augur/internal/backend"
	openaibackend "github.com/vanshamara/Augur/internal/backend/openai"
	appconfig "github.com/vanshamara/Augur/internal/config"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/httpapi"
	"github.com/vanshamara/Augur/internal/live"
	"github.com/vanshamara/Augur/internal/openaiapi"
	"github.com/vanshamara/Augur/internal/quality"
	"github.com/vanshamara/Augur/internal/router"
)

func main() {
	config, err := readConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	client, err := openaiapi.New(openaiapi.Config{
		BaseURL:   config.OpenAI.BaseURL,
		APIKeyEnv: config.OpenAI.APIKeyEnv,
	})
	if err != nil {
		log.Fatal(err)
	}

	backends, err := buildBackends(config.Backends, client)
	if err != nil {
		log.Fatal(err)
	}
	ids := backendIDs(config.Backends)
	routing, err := buildRouter(config, ids)
	if err != nil {
		log.Fatal(err)
	}
	filters, err := buildFilters(config, ids)
	if err != nil {
		log.Fatal(err)
	}
	singleFlight, singleFlightKey := buildSingleFlight(config.DataPlane.SingleFlight)

	gateway, err := dataplane.New(dataplane.Config{
		Router:          routing.Router,
		Backends:        backends,
		Filters:         filters,
		Hedge:           buildHedge(config.DataPlane.Hedge),
		SingleFlight:    singleFlight,
		SingleFlightKey: singleFlightKey,
	})
	if err != nil {
		log.Fatal(err)
	}
	servingGateway, err := buildLiveGateway(config, gateway, routing.Bandit, client)
	if err != nil {
		log.Fatal(err)
	}
	server, err := httpapi.New(httpapi.Config{
		Gateway:  servingGateway,
		Defaults: requestDefaults(config.Budgets),
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("augur listening on %s", config.Server.Addr)
	log.Fatal(http.ListenAndServe(config.Server.Addr, server))
}

func readConfig(getenv func(string) string) (appconfig.App, error) {
	if path := strings.TrimSpace(getenv("AUGUR_CONFIG")); path != "" {
		return appconfig.LoadFile(path)
	}

	addr := strings.TrimSpace(getenv("AUGUR_ADDR"))
	if addr == "" {
		addr = appconfig.DefaultAddr
	}
	backends, err := parseBackends(getenv("AUGUR_BACKENDS"))
	if err != nil {
		return appconfig.App{}, err
	}
	return appconfig.App{
		Server: appconfig.Server{
			Addr: addr,
		},
		OpenAI: appconfig.OpenAI{
			BaseURL:   strings.TrimSpace(getenv("AUGUR_OPENAI_BASE_URL")),
			APIKeyEnv: "OPENAI_API_KEY",
		},
		Router: appconfig.Router{
			Type: "round_robin",
		},
		Backends: backends,
	}, nil
}

func parseBackends(value string) ([]appconfig.Backend, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("AUGUR_BACKENDS is required, for example AUGUR_BACKENDS=fast=your-model-id,stable=your-second-model-id")
	}

	parts := strings.Split(value, ",")
	backends := make([]appconfig.Backend, 0, len(parts))
	for _, part := range parts {
		spec, err := parseBackendSpec(part)
		if err != nil {
			return nil, err
		}
		backends = append(backends, spec)
	}
	return backends, nil
}

func parseBackendSpec(value string) (appconfig.Backend, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return appconfig.Backend{}, errors.New("backend spec cannot be empty")
	}
	if !strings.Contains(value, "=") {
		return appconfig.Backend{ID: core.BackendID(value), Model: value}, nil
	}

	parts := strings.SplitN(value, "=", 2)
	id := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])
	if id == "" || model == "" {
		return appconfig.Backend{}, fmt.Errorf("invalid backend spec %q", value)
	}
	return appconfig.Backend{ID: core.BackendID(id), Model: model}, nil
}

func buildBackends(specs []appconfig.Backend, client *openaiapi.Client) ([]backend.Backend, error) {
	backends := make([]backend.Backend, 0, len(specs))
	for _, spec := range specs {
		b, err := openaibackend.New(openaibackend.Config{
			ID:                  spec.ID,
			Model:               spec.Model,
			Client:              client,
			InputCostPerToken:   spec.InputCostPerToken,
			OutputCostPerToken:  spec.OutputCostPerToken,
			MaxCompletionTokens: spec.MaxCompletionTokens,
		})
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return backends, nil
}

type routerBuild struct {
	Router router.Router
	Bandit *control.BanditRouter
}

func backendIDs(backends []appconfig.Backend) []core.BackendID {
	ids := make([]core.BackendID, len(backends))
	for i, b := range backends {
		ids[i] = b.ID
	}
	return ids
}

func buildRouter(config appconfig.App, ids []core.BackendID) (routerBuild, error) {
	switch config.Router.Type {
	case "static":
		return routerBuild{Router: router.NewStatic(ids[0])}, nil
	case "round_robin", "round-robin":
		return routerBuild{Router: router.NewRoundRobin()}, nil
	case "least_loaded", "least-loaded":
		return routerBuild{Router: router.NewLeastLoaded(ids)}, nil
	case "ewma":
		return routerBuild{Router: router.NewEWMA(ids, config.Router.Alpha)}, nil
	case "cost_aware", "cost-aware":
		return routerBuild{Router: router.NewCostAware(prices(config))}, nil
	case "p2c":
		return routerBuild{Router: router.NewP2CWithWindow(ids, config.Router.Alpha, config.Router.Seed, config.Router.P2CWindow)}, nil
	case "litellm_shuffle", "litellm-shuffle":
		return routerBuild{Router: router.NewLiteLLMShuffle(config.Router.Weights, config.Router.Seed)}, nil
	case "envoy_least_request", "envoy-least-request":
		return routerBuild{Router: router.NewEnvoyLeastRequest(ids, config.Router.Weights, config.Router.Seed)}, nil
	case "bandit":
		bandit := control.NewBanditRouter(control.BanditConfig{
			Policy:         control.NewPolicy(config.Policy),
			Backends:       ids,
			Seed:           config.Router.Seed,
			Tau:            config.Learning.Tau.Duration,
			PriorPrecision: config.Learning.PriorPrecision,
		})
		return routerBuild{Router: bandit, Bandit: bandit}, nil
	default:
		return routerBuild{}, fmt.Errorf("unsupported router %q", config.Router.Type)
	}
}

func prices(config appconfig.App) map[core.BackendID]float64 {
	if len(config.DataPlane.Prices) > 0 {
		return config.DataPlane.Prices
	}
	out := make(map[core.BackendID]float64, len(config.Backends))
	for _, b := range config.Backends {
		out[b.ID] = pricePerToken(b)
	}
	return out
}

func pricePerToken(backend appconfig.Backend) float64 {
	if backend.InputCostPerToken > 0 && backend.OutputCostPerToken > 0 {
		return (backend.InputCostPerToken + backend.OutputCostPerToken) / 2
	}
	if backend.OutputCostPerToken > 0 {
		return backend.OutputCostPerToken
	}
	return backend.InputCostPerToken
}

func buildFilters(config appconfig.App, ids []core.BackendID) ([]dataplane.Filter, error) {
	filters := make([]dataplane.Filter, 0, len(config.DataPlane.Filters))
	for _, name := range config.DataPlane.Filters {
		switch name {
		case "health":
			health := dataplane.NewHealthFilter(ids)
			for id, ok := range config.DataPlane.Health {
				health.Set(id, ok)
			}
			filters = append(filters, health)
		case "circuit":
			filters = append(filters, dataplane.NewCircuitBreaker(ids, dataplane.CircuitConfig{
				FailureThreshold: config.DataPlane.Circuit.FailureThreshold,
				RecoveryAfter:    config.DataPlane.Circuit.RecoveryAfter.Duration,
				HalfOpenMax:      config.DataPlane.Circuit.HalfOpenMax,
			}))
		case "concurrency":
			filters = append(filters, dataplane.NewAdaptiveLimiter(ids, dataplane.LimitConfig{
				InitialLimit:    config.DataPlane.Concurrency.InitialLimit,
				MinLimit:        config.DataPlane.Concurrency.MinLimit,
				MaxLimit:        config.DataPlane.Concurrency.MaxLimit,
				TargetLatencyMs: config.DataPlane.Concurrency.TargetLatencyMs,
			}))
		default:
			return nil, fmt.Errorf("unsupported filter %q", name)
		}
	}
	return filters, nil
}

func buildHedge(config appconfig.Hedge) dataplane.HedgeConfig {
	return dataplane.HedgeConfig{
		Enabled:     config.Enabled,
		Delay:       config.Delay.Duration,
		MaxInFlight: config.MaxInFlight,
	}
}

func buildSingleFlight(config appconfig.SingleFlight) (*dataplane.SingleFlight, dataplane.KeyFunc) {
	if !config.Enabled {
		return nil, nil
	}
	switch config.Key {
	case "prompt":
		return dataplane.NewSingleFlight(), func(req core.Request) string {
			return req.Prompt
		}
	case "request_id":
		return dataplane.NewSingleFlight(), func(req core.Request) string {
			return req.ID
		}
	default:
		return nil, nil
	}
}

func requestDefaults(config appconfig.Budgets) httpapi.RequestDefaults {
	return httpapi.RequestDefaults{
		LatencyBudgetMs:     config.LatencyBudgetMs,
		CostBudgetUSD:       config.CostBudgetUSD,
		MaxCompletionTokens: config.MaxCompletionTokens,
		Temperature:         config.Temperature,
	}
}

func buildLiveGateway(config appconfig.App, gateway live.Gateway, bandit *control.BanditRouter, client *openaiapi.Client) (live.Gateway, error) {
	if !config.Learning.Enabled {
		return gateway, nil
	}
	if bandit == nil {
		return nil, errors.New("live learning requires router.type to be bandit")
	}

	var scorer quality.Scorer
	if config.Learning.Judge.Enabled {
		judge, err := quality.NewJudgeScorer(quality.JudgeConfig{
			Model:      config.Learning.Judge.Model,
			Client:     client,
			SampleRate: 1,
			Seed:       config.Learning.Judge.Seed,
		})
		if err != nil {
			return nil, err
		}
		scorer = judge
	}
	return live.New(live.Config{
		Gateway:   gateway,
		Bandit:    bandit,
		Scorer:    scorer,
		Seed:      config.Learning.Judge.Seed,
		QueueSize: config.Learning.QueueSize,
	})
}
