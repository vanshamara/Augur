package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vanshamara/Augur/internal/backend"
	openaibackend "github.com/vanshamara/Augur/internal/backend/openai"
	appconfig "github.com/vanshamara/Augur/internal/config"
	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/httpapi"
	"github.com/vanshamara/Augur/internal/live"
	"github.com/vanshamara/Augur/internal/openaiapi"
	"github.com/vanshamara/Augur/internal/persist"
	"github.com/vanshamara/Augur/internal/quality"
	"github.com/vanshamara/Augur/internal/router"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	config, err := readConfig(getenv)
	if err != nil {
		return err
	}

	client, err := openaiapi.New(openaiapi.Config{
		BaseURL:   config.OpenAI.BaseURL,
		APIKeyEnv: config.OpenAI.APIKeyEnv,
	})
	if err != nil {
		return err
	}

	backends, err := buildBackends(config.Backends, client)
	if err != nil {
		return err
	}
	ids := backendIDs(config.Backends)
	routing, err := buildRouter(config, ids)
	if err != nil {
		return err
	}
	filters, err := buildFilters(config, ids)
	if err != nil {
		return err
	}
	singleFlight, singleFlightKey := buildSingleFlight(config.DataPlane.SingleFlight)

	gateway, err := dataplane.New(dataplane.Config{
		Router:          routing.Router,
		Backends:        backends,
		Routes:          buildRouteRules(config.Routes),
		Capabilities:    buildBackendCapabilities(config.Backends),
		Canary:          buildCanaryConfig(config.Canary),
		Filters:         filters,
		Hedge:           buildHedge(config.DataPlane.Hedge),
		SingleFlight:    singleFlight,
		SingleFlightKey: singleFlightKey,
	})
	if err != nil {
		return err
	}
	servingGateway, err := buildLiveGateway(config, gateway, routing.Bandit, client)
	if err != nil {
		return err
	}
	defer closeGateway(servingGateway)

	apiServer, err := httpapi.New(httpapi.Config{
		Gateway:        servingGateway,
		AuthKeys:       gatewayAuthKeys(getenv),
		Defaults:       requestDefaults(config.Budgets),
		TenantHeader:   config.Tenants.Header,
		DefaultTenant:  config.Tenants.DefaultTenant,
		TenantDefaults: tenantRequestDefaults(config),
		MaxBodyBytes:   config.Server.MaxBodyBytes,
		Ready: func(ctx context.Context) bool {
			return len(backends) > 0
		},
	})
	if err != nil {
		return err
	}

	server := buildHTTPServer(config.Server, apiServer)
	log.Printf("augur listening on %s", config.Server.Addr)
	return runHTTPServer(ctx, server, config.Server.ShutdownTimeout.Duration)
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
			Addr:            addr,
			MaxBodyBytes:    appconfig.DefaultMaxBodyBytes,
			ReadTimeout:     appconfig.Duration{Duration: appconfig.DefaultReadTimeout},
			WriteTimeout:    appconfig.Duration{Duration: appconfig.DefaultWriteTimeout},
			IdleTimeout:     appconfig.Duration{Duration: appconfig.DefaultIdleTimeout},
			ShutdownTimeout: appconfig.Duration{Duration: appconfig.DefaultShutdownTimeout},
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

func gatewayAuthKeys(getenv func(string) string) []string {
	value := strings.TrimSpace(getenv("AUGUR_GATEWAY_API_KEYS"))
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
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
		case "tenant":
			filters = append(filters, dataplane.NewTenantLimiter(tenantLimitConfig(config.Tenants)))
		default:
			return nil, fmt.Errorf("unsupported filter %q", name)
		}
	}
	return filters, nil
}

func buildRouteRules(routes []appconfig.Route) []dataplane.RouteRule {
	out := make([]dataplane.RouteRule, 0, len(routes))
	for _, route := range routes {
		out = append(out, dataplane.RouteRule{
			Name: route.Name,
			Match: dataplane.RouteMatch{
				TaskTypes: route.Match.TaskTypes,
				Tenants:   route.Match.Tenants,
				UserTiers: route.Match.UserTiers,
			},
			Candidates: routeCandidateBackends(route.Candidates),
			Fallbacks:  routeCandidateBackends(route.Fallbacks),
			Canary: dataplane.CanaryRule{
				Backend:   route.Canary.Backend,
				Percent:   route.Canary.Percent,
				StickyKey: route.Canary.StickyKey,
				Shadow:    route.Canary.Shadow,
			},
		})
	}
	return out
}

func routeCandidateBackends(candidates []appconfig.RouteCandidate) []core.BackendID {
	out := make([]core.BackendID, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Backend)
	}
	return out
}

func buildBackendCapabilities(backends []appconfig.Backend) map[core.BackendID][]core.RequestType {
	out := make(map[core.BackendID][]core.RequestType, len(backends))
	for _, backend := range backends {
		out[backend.ID] = append([]core.RequestType(nil), backend.Capabilities...)
	}
	return out
}

func buildCanaryConfig(config appconfig.Canary) dataplane.CanaryConfig {
	return dataplane.CanaryConfig{
		Rollback: config.RollbackConfig(),
	}
}

func buildHedge(config appconfig.Hedge) dataplane.HedgeConfig {
	return dataplane.HedgeConfig{
		Enabled:           config.Enabled,
		Delay:             config.Delay.Duration,
		MaxInFlight:       config.MaxInFlight,
		BudgetFraction:    config.BudgetFraction,
		TriggerPercentile: config.TriggerPercentile,
		MaxExtraCalls:     config.MaxExtraCalls,
	}
}

func buildSingleFlight(config appconfig.SingleFlight) (*dataplane.SingleFlight, dataplane.KeyFunc) {
	if !config.Enabled {
		return nil, nil
	}
	switch config.Key {
	case "prompt":
		return dataplane.NewSingleFlight(), func(req core.Request) string {
			return tenantScopedKey(req, req.Prompt)
		}
	case "request_id":
		return dataplane.NewSingleFlight(), func(req core.Request) string {
			return tenantScopedKey(req, req.ID)
		}
	default:
		return nil, nil
	}
}

func tenantScopedKey(req core.Request, value string) string {
	if value == "" {
		return ""
	}
	tenant := req.TenantID
	if tenant == "" {
		tenant = "default"
	}
	return tenant + "\x00" + value
}

func requestDefaults(config appconfig.Budgets) httpapi.RequestDefaults {
	return httpapi.RequestDefaults{
		LatencyBudgetMs:     config.LatencyBudgetMs,
		CostBudgetUSD:       config.CostBudgetUSD,
		MaxCompletionTokens: config.MaxCompletionTokens,
		Temperature:         config.Temperature,
	}
}

func tenantRequestDefaults(config appconfig.App) map[string]httpapi.RequestDefaults {
	out := map[string]httpapi.RequestDefaults{}
	defaults := tenantPolicyDefaults(config.Tenants.Defaults.Policy)
	if !defaults.Empty() {
		out[config.Tenants.DefaultTenant] = defaults
	}
	for tenant, spec := range config.Tenants.Overrides {
		defaults := tenantPolicyDefaults(spec.Policy)
		if !defaults.Empty() {
			out[tenant] = defaults
		}
	}
	return out
}

func tenantPolicyDefaults(config appconfig.TenantPolicy) httpapi.RequestDefaults {
	return httpapi.RequestDefaults{
		LatencyBudgetMs:     config.LatencyBudgetMs,
		CostBudgetUSD:       config.CostBudgetUSD,
		MaxCompletionTokens: config.MaxCompletionTokens,
		Temperature:         config.Temperature,
		UserTier:            config.UserTier,
	}
}

func tenantLimitConfig(config appconfig.Tenants) dataplane.TenantLimitConfig {
	limits := make(map[string]dataplane.TenantLimit, len(config.Overrides))
	for tenant, spec := range config.Overrides {
		limits[tenant] = dataplane.TenantLimit{
			MaxInFlight: spec.MaxInFlight,
			MaxCostUSD:  spec.MaxCostUSD,
		}
	}
	return dataplane.TenantLimitConfig{
		DefaultTenant: config.DefaultTenant,
		Defaults: dataplane.TenantLimit{
			MaxInFlight: config.Defaults.MaxInFlight,
			MaxCostUSD:  config.Defaults.MaxCostUSD,
		},
		Tenants: limits,
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
			SampleRate: config.Policy.Exploration.JudgeSampleRate,
			Seed:       config.Learning.Judge.Seed,
		})
		if err != nil {
			return nil, err
		}
		scorer = judge
	}
	store, err := buildStateStore(config, bandit)
	if err != nil {
		return nil, err
	}
	return live.New(live.Config{
		Gateway:   gateway,
		Bandit:    bandit,
		Scorer:    scorer,
		Store:     store,
		Seed:      config.Learning.Judge.Seed,
		QueueSize: config.Learning.QueueSize,
		SaveEvery: config.Learning.Persistence.SaveEvery,
	})
}

func buildStateStore(config appconfig.App, bandit *control.BanditRouter) (live.StateStore, error) {
	if !config.Learning.Persistence.Enabled {
		return nil, nil
	}

	store, err := persist.NewFileStore(persist.FileConfig{
		Path:     config.Learning.Persistence.Path,
		PolicyID: control.NewPolicy(config.Policy).ID(),
		Backends: backendIDs(config.Backends),
	})
	if err != nil {
		return nil, err
	}

	state, err := store.Load()
	if err == nil {
		bandit.RestoreLearnedState(state)
		return store, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return store, nil
}

func buildHTTPServer(config appconfig.Server, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         config.Addr,
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout.Duration,
		WriteTimeout: config.WriteTimeout.Duration,
		IdleTimeout:  config.IdleTimeout.Duration,
	}
}

func runHTTPServer(ctx context.Context, server *http.Server, shutdownTimeout time.Duration) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return serveHTTPServer(ctx, server, listener, shutdownTimeout)
}

func serveHTTPServer(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	if shutdownTimeout <= 0 {
		shutdownTimeout = appconfig.DefaultShutdownTimeout
	}

	done := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-done
	case err := <-done:
		return err
	}
}

func closeGateway(gateway interface{}) {
	closer, ok := gateway.(interface{ Close() })
	if ok {
		closer.Close()
	}
}
