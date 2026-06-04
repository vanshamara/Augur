package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"gopkg.in/yaml.v3"
)

const (
	DefaultAddr            = "127.0.0.1:8080"
	DefaultMaxBodyBytes    = 1_048_576
	DefaultReadTimeout     = 5 * time.Second
	DefaultWriteTimeout    = 30 * time.Second
	DefaultIdleTimeout     = 2 * time.Minute
	DefaultShutdownTimeout = 10 * time.Second
	DefaultDecisionLogSize = 256
)

type App struct {
	Server    Server               `json:"server"`
	OpenAI    OpenAI               `json:"openai"`
	Backends  []Backend            `json:"backends"`
	Routes    []Route              `json:"routes"`
	Router    Router               `json:"router"`
	DataPlane DataPlane            `json:"data_plane"`
	Learning  Learning             `json:"learning"`
	Pricing   Pricing              `json:"pricing"`
	Canary    Canary               `json:"canary"`
	Tenants   Tenants              `json:"tenants"`
	Policy    control.PolicyConfig `json:"policy"`
	Budgets   Budgets              `json:"budgets"`
}

type Server struct {
	Addr            string   `json:"addr"`
	MaxBodyBytes    int64    `json:"max_body_bytes"`
	ReadTimeout     Duration `json:"read_timeout"`
	WriteTimeout    Duration `json:"write_timeout"`
	IdleTimeout     Duration `json:"idle_timeout"`
	ShutdownTimeout Duration `json:"shutdown_timeout"`
}

type OpenAI struct {
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env"`
}

type Backend struct {
	ID                  core.BackendID     `json:"id"`
	Model               string             `json:"model"`
	Capabilities        []core.RequestType `json:"capabilities"`
	HealthPath          string             `json:"health_path"`
	Timeout             Duration           `json:"timeout"`
	InputCostPerToken   float64            `json:"input_cost_per_token"`
	OutputCostPerToken  float64            `json:"output_cost_per_token"`
	MaxCompletionTokens int                `json:"max_completion_tokens"`
}

type Route struct {
	Name       string           `json:"name"`
	Match      RouteMatch       `json:"match"`
	Candidates []RouteCandidate `json:"candidates"`
	Fallbacks  []RouteCandidate `json:"fallbacks"`
	Canary     RouteCanary      `json:"canary"`
}

type RouteMatch struct {
	TaskTypes []core.RequestType `json:"task_types"`
	Tenants   []string           `json:"tenants"`
	UserTiers []string           `json:"user_tiers"`
}

type RouteCandidate struct {
	Backend core.BackendID `json:"backend"`
}

type RouteCanary struct {
	Backend   core.BackendID `json:"backend"`
	Percent   float64        `json:"percent"`
	StickyKey string         `json:"sticky_key"`
	Shadow    bool           `json:"shadow"`
}

type Pricing struct {
	Models map[string]ModelPrice `json:"models"`
}

type ModelPrice struct {
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
}

type Router struct {
	Type      string                     `json:"type"`
	Seed      uint64                     `json:"seed"`
	Alpha     float64                    `json:"alpha"`
	P2CWindow int                        `json:"p2c_window"`
	Weights   map[core.BackendID]float64 `json:"weights"`
}

type DataPlane struct {
	Filters      []string                   `json:"filters"`
	Circuit      Circuit                    `json:"circuit"`
	Concurrency  Concurrency                `json:"concurrency"`
	Hedge        Hedge                      `json:"hedge"`
	SingleFlight SingleFlight               `json:"single_flight"`
	HealthCheck  HealthCheck                `json:"health_check"`
	Health       map[core.BackendID]bool    `json:"health"`
	Prices       map[core.BackendID]float64 `json:"prices"`
	DecisionLog  DecisionLog                `json:"decision_log"`
}

type DecisionLog struct {
	Enabled bool `json:"enabled"`
	Size    int  `json:"size"`
}

type Learning struct {
	Enabled        bool        `json:"enabled"`
	Tau            Duration    `json:"tau"`
	PriorPrecision float64     `json:"prior_precision"`
	QueueSize      int         `json:"queue_size"`
	Persistence    Persistence `json:"persistence"`
	Judge          Judge       `json:"judge"`
}

type Persistence struct {
	Enabled   bool   `json:"enabled"`
	Path      string `json:"path"`
	SaveEvery int    `json:"save_every"`
}

type Judge struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
	Seed    uint64 `json:"seed"`
}

type Circuit struct {
	FailureThreshold int      `json:"failure_threshold"`
	RecoveryAfter    Duration `json:"recovery_after"`
	HalfOpenMax      int      `json:"half_open_max"`
}

type Concurrency struct {
	InitialLimit    int64   `json:"initial_limit"`
	MinLimit        int64   `json:"min_limit"`
	MaxLimit        int64   `json:"max_limit"`
	TargetLatencyMs float64 `json:"target_latency_ms"`
}

type Hedge struct {
	Enabled           bool     `json:"enabled"`
	Delay             Duration `json:"delay"`
	MaxInFlight       int64    `json:"max_in_flight"`
	BudgetFraction    *float64 `json:"budget_fraction"`
	TriggerPercentile int      `json:"trigger_percentile"`
	MaxExtraCalls     int      `json:"max_extra_calls"`
}

type SingleFlight struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key"`
}

type HealthCheck struct {
	Enabled          bool     `json:"enabled"`
	Interval         Duration `json:"interval"`
	Timeout          Duration `json:"timeout"`
	FailureThreshold int      `json:"failure_threshold"`
	SuccessThreshold int      `json:"success_threshold"`
}

type Canary struct {
	P95RegressionRatio float64 `json:"p95_regression_ratio"`
	MaxErrorRate       float64 `json:"max_error_rate"`
	MinSamples         int     `json:"min_samples"`
}

type Tenants struct {
	Header        string            `json:"header"`
	DefaultTenant string            `json:"default_tenant"`
	Defaults      Tenant            `json:"defaults"`
	Overrides     map[string]Tenant `json:"overrides"`
}

type Tenant struct {
	MaxInFlight int64        `json:"max_in_flight"`
	MaxCostUSD  float64      `json:"max_cost_usd"`
	Policy      TenantPolicy `json:"policy"`
}

type TenantPolicy struct {
	LatencyBudgetMs     int      `json:"latency_budget_ms"`
	CostBudgetUSD       float64  `json:"cost_budget_usd"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	Temperature         *float64 `json:"temperature"`
	UserTier            string   `json:"user_tier"`
}

type Budgets struct {
	LatencyBudgetMs     int      `json:"latency_budget_ms"`
	CostBudgetUSD       float64  `json:"cost_budget_usd"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	Temperature         *float64 `json:"temperature"`
}

type Duration struct {
	time.Duration
}

func LoadFile(path string) (App, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return App{}, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return ParseYAML(data)
	default:
		return Parse(data)
	}
}

func ParseYAML(data []byte) (App, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return App{}, err
	}
	normalized, err := normalizeYAML(raw)
	if err != nil {
		return App{}, err
	}
	jsonData, err := json.Marshal(normalized)
	if err != nil {
		return App{}, err
	}
	return Parse(jsonData)
}

func normalizeYAML(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized, err := normalizeYAML(item)
			if err != nil {
				return nil, err
			}
			out[key] = normalized
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			text, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("yaml map key %v must be a string", key)
			}
			normalized, err := normalizeYAML(item)
			if err != nil {
				return nil, err
			}
			out[text] = normalized
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			normalized, err := normalizeYAML(item)
			if err != nil {
				return nil, err
			}
			out[i] = normalized
		}
		return out, nil
	default:
		return value, nil
	}
}

func Parse(data []byte) (App, error) {
	var app App
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&app); err != nil {
		return App{}, err
	}
	return app.withDefaults()
}

func (a App) withDefaults() (App, error) {
	if strings.TrimSpace(a.Server.Addr) == "" {
		a.Server.Addr = DefaultAddr
	}
	if a.Server.MaxBodyBytes < 0 {
		return App{}, errors.New("server max_body_bytes cannot be negative")
	}
	if a.Server.MaxBodyBytes == 0 {
		a.Server.MaxBodyBytes = DefaultMaxBodyBytes
	}
	var err error
	a.Server.ReadTimeout, err = defaultServerDuration(a.Server.ReadTimeout, DefaultReadTimeout, "read_timeout")
	if err != nil {
		return App{}, err
	}
	a.Server.WriteTimeout, err = defaultServerDuration(a.Server.WriteTimeout, DefaultWriteTimeout, "write_timeout")
	if err != nil {
		return App{}, err
	}
	a.Server.IdleTimeout, err = defaultServerDuration(a.Server.IdleTimeout, DefaultIdleTimeout, "idle_timeout")
	if err != nil {
		return App{}, err
	}
	a.Server.ShutdownTimeout, err = defaultServerDuration(a.Server.ShutdownTimeout, DefaultShutdownTimeout, "shutdown_timeout")
	if err != nil {
		return App{}, err
	}
	if strings.TrimSpace(a.OpenAI.APIKeyEnv) == "" {
		a.OpenAI.APIKeyEnv = "OPENAI_API_KEY"
	}
	if strings.TrimSpace(a.Router.Type) == "" {
		a.Router.Type = "round_robin"
	}
	if a.Router.Alpha == 0 {
		a.Router.Alpha = 0.2
	}
	if len(a.Backends) == 0 {
		return App{}, errors.New("at least one backend is required")
	}
	for i := range a.Backends {
		if strings.TrimSpace(a.Backends[i].Model) == "" {
			return App{}, fmt.Errorf("backend %d model is required", i)
		}
		if a.Backends[i].ID == "" {
			a.Backends[i].ID = core.BackendID(a.Backends[i].Model)
		}
		a.Backends[i].HealthPath = strings.TrimSpace(a.Backends[i].HealthPath)
		if a.Backends[i].Timeout.Duration < 0 {
			return App{}, fmt.Errorf("backend %q timeout cannot be negative", a.Backends[i].ID)
		}
		if err := validateBackendCapabilities(a.Backends[i]); err != nil {
			return App{}, err
		}
	}
	a.applyPricingTable()
	if err := validatePricing(a.Pricing, a.Backends); err != nil {
		return App{}, err
	}
	if err := validateRoutes(a.Routes, a.Backends); err != nil {
		return App{}, err
	}
	if err := validateRouter(a.Router.Type); err != nil {
		return App{}, err
	}
	if err := validateFilters(a.DataPlane.Filters); err != nil {
		return App{}, err
	}
	if a.DataPlane.SingleFlight.Enabled && a.DataPlane.SingleFlight.Key == "" {
		a.DataPlane.SingleFlight.Key = "prompt"
	}
	if a.DataPlane.SingleFlight.Enabled {
		switch a.DataPlane.SingleFlight.Key {
		case "prompt", "request_id":
		default:
			return App{}, fmt.Errorf("unsupported single flight key %q", a.DataPlane.SingleFlight.Key)
		}
	}
	if a.DataPlane.Hedge.BudgetFraction != nil {
		if *a.DataPlane.Hedge.BudgetFraction < 0 || *a.DataPlane.Hedge.BudgetFraction > 1 {
			return App{}, errors.New("data_plane hedge budget_fraction must be between 0 and 1")
		}
	}
	if a.DataPlane.Hedge.TriggerPercentile < 0 || a.DataPlane.Hedge.TriggerPercentile > 100 {
		return App{}, errors.New("data_plane hedge trigger_percentile must be between 0 and 100")
	}
	if a.DataPlane.Hedge.MaxExtraCalls < 0 {
		return App{}, errors.New("data_plane hedge max_extra_calls cannot be negative")
	}
	if err := validateHealthCheck(a.DataPlane.HealthCheck); err != nil {
		return App{}, err
	}
	if a.DataPlane.DecisionLog.Size < 0 {
		return App{}, errors.New("data_plane decision_log size cannot be negative")
	}
	if a.DataPlane.DecisionLog.Enabled && a.DataPlane.DecisionLog.Size == 0 {
		a.DataPlane.DecisionLog.Size = DefaultDecisionLogSize
	}
	a.Tenants.Header = strings.TrimSpace(a.Tenants.Header)
	if a.Tenants.Header == "" {
		a.Tenants.Header = "X-Augur-Tenant"
	}
	a.Tenants.DefaultTenant = strings.TrimSpace(a.Tenants.DefaultTenant)
	if a.Tenants.DefaultTenant == "" {
		a.Tenants.DefaultTenant = "default"
	}
	if err := validateTenant(a.Tenants.DefaultTenant, a.Tenants.Defaults); err != nil {
		return App{}, err
	}
	for tenant, config := range a.Tenants.Overrides {
		if strings.TrimSpace(tenant) == "" {
			return App{}, errors.New("tenant override name cannot be empty")
		}
		if err := validateTenant(tenant, config); err != nil {
			return App{}, err
		}
	}
	if a.Learning.QueueSize <= 0 {
		a.Learning.QueueSize = 1024
	}
	if a.Learning.Persistence.SaveEvery < 0 {
		return App{}, errors.New("learning persistence save_every cannot be negative")
	}
	if a.Learning.Persistence.SaveEvery == 0 {
		a.Learning.Persistence.SaveEvery = 1
	}
	if a.Learning.Persistence.Enabled && !a.Learning.Enabled {
		return App{}, errors.New("learning must be enabled when persistence is enabled")
	}
	if a.Learning.Persistence.Enabled && strings.TrimSpace(a.Learning.Persistence.Path) == "" {
		return App{}, errors.New("learning persistence path is required when persistence is enabled")
	}
	if a.Learning.Judge.Enabled && strings.TrimSpace(a.Learning.Judge.Model) == "" {
		return App{}, errors.New("learning judge model is required when judge is enabled")
	}
	if a.Learning.Judge.Enabled && a.Policy.Exploration.JudgeSampleRate <= 0 {
		return App{}, errors.New("policy exploration judge_sample_rate must be positive when judge is enabled")
	}
	return a, nil
}

func (c Canary) RollbackConfig() control.RollbackConfig {
	return control.RollbackConfig{
		P95RegressionRatio: c.P95RegressionRatio,
		MaxErrorRate:       c.MaxErrorRate,
		MinSamples:         c.MinSamples,
	}
}

func validateTenant(name string, tenant Tenant) error {
	if tenant.MaxInFlight < 0 {
		return fmt.Errorf("tenant %q max_in_flight cannot be negative", name)
	}
	if tenant.MaxCostUSD < 0 {
		return fmt.Errorf("tenant %q max_cost_usd cannot be negative", name)
	}
	if tenant.Policy.LatencyBudgetMs < 0 {
		return fmt.Errorf("tenant %q policy latency_budget_ms cannot be negative", name)
	}
	if tenant.Policy.CostBudgetUSD < 0 {
		return fmt.Errorf("tenant %q policy cost_budget_usd cannot be negative", name)
	}
	if tenant.Policy.MaxCompletionTokens < 0 {
		return fmt.Errorf("tenant %q policy max_completion_tokens cannot be negative", name)
	}
	return nil
}

func validateHealthCheck(config HealthCheck) error {
	if config.Interval.Duration < 0 {
		return errors.New("data_plane health_check interval cannot be negative")
	}
	if config.Timeout.Duration < 0 {
		return errors.New("data_plane health_check timeout cannot be negative")
	}
	if config.FailureThreshold < 0 {
		return errors.New("data_plane health_check failure_threshold cannot be negative")
	}
	if config.SuccessThreshold < 0 {
		return errors.New("data_plane health_check success_threshold cannot be negative")
	}
	return nil
}

func validateRoutes(routes []Route, backends []Backend) error {
	if len(routes) == 0 {
		return nil
	}

	backendIDs := map[core.BackendID]bool{}
	for _, backend := range backends {
		backendIDs[backend.ID] = true
	}

	names := map[string]bool{}
	hasDefault := false
	for i, route := range routes {
		name := strings.TrimSpace(route.Name)
		if name == "" {
			return fmt.Errorf("route %d name is required", i)
		}
		if names[name] {
			return fmt.Errorf("duplicate route name %q", name)
		}
		names[name] = true

		if len(route.Candidates) == 0 {
			return fmt.Errorf("route %q must include at least one candidate", name)
		}
		if err := validateRouteBackends(name, "candidate", route.Candidates, backendIDs); err != nil {
			return err
		}
		if err := validateRouteBackends(name, "fallback", route.Fallbacks, backendIDs); err != nil {
			return err
		}
		if err := validateRouteCanary(name, route.Canary, backendIDs); err != nil {
			return err
		}
		if err := validateRouteMatch(name, route.Match); err != nil {
			return err
		}
		if emptyRouteMatch(route.Match) {
			if hasDefault {
				return errors.New("only one default route can have an empty match")
			}
			hasDefault = true
		}
	}
	return nil
}

func validateRouteCanary(name string, canary RouteCanary, backendIDs map[core.BackendID]bool) error {
	if canary.Backend == "" && canary.Percent == 0 && strings.TrimSpace(canary.StickyKey) == "" && !canary.Shadow {
		return nil
	}
	if canary.Backend == "" {
		return fmt.Errorf("route %q canary backend is required", name)
	}
	if !backendIDs[canary.Backend] {
		return fmt.Errorf("route %q references unknown canary backend %q", name, canary.Backend)
	}
	if canary.Percent < 0 || canary.Percent > 100 {
		return fmt.Errorf("route %q canary percent must be between 0 and 100", name)
	}
	switch strings.TrimSpace(canary.StickyKey) {
	case "", "request_id", "tenant_id", "user_id", "tenant_and_request", "tenant_and_user":
		return nil
	default:
		return fmt.Errorf("route %q has unsupported canary sticky_key %q", name, canary.StickyKey)
	}
}

func validateRouteBackends(name string, role string, entries []RouteCandidate, backendIDs map[core.BackendID]bool) error {
	for _, entry := range entries {
		if entry.Backend == "" {
			return fmt.Errorf("route %q %s backend is required", name, role)
		}
		if !backendIDs[entry.Backend] {
			return fmt.Errorf("route %q references unknown %s backend %q", name, role, entry.Backend)
		}
	}
	return nil
}

func validateBackendCapabilities(backend Backend) error {
	for _, capability := range backend.Capabilities {
		if !supportedRequestType(capability) {
			return fmt.Errorf("backend %q has unsupported capability %q", backend.ID, capability)
		}
	}
	return nil
}

func validateRouteMatch(name string, match RouteMatch) error {
	for _, taskType := range match.TaskTypes {
		if !supportedRequestType(taskType) {
			return fmt.Errorf("route %q has unsupported task type %q", name, taskType)
		}
	}
	for _, tenant := range match.Tenants {
		if strings.TrimSpace(tenant) == "" {
			return fmt.Errorf("route %q tenant match cannot be empty", name)
		}
	}
	for _, tier := range match.UserTiers {
		if strings.TrimSpace(tier) == "" {
			return fmt.Errorf("route %q user tier match cannot be empty", name)
		}
	}
	return nil
}

func supportedRequestType(value core.RequestType) bool {
	switch value {
	case core.Chat, core.Reasoning, core.Coding, core.Embedding:
		return true
	default:
		return false
	}
}

func emptyRouteMatch(match RouteMatch) bool {
	return len(match.TaskTypes) == 0 && len(match.Tenants) == 0 && len(match.UserTiers) == 0
}

// maxCostPerToken guards against a common unit mistake. Prices are per single
// token, so any value at or above one dollar per token is almost always a price
// meant for a million or a thousand tokens instead.
const maxCostPerToken = 1.0

// validatePricing rejects negative prices and prices that look like the wrong
// unit, so config errors point at the real problem instead of silent overcharge.
func validatePricing(pricing Pricing, backends []Backend) error {
	for model, price := range pricing.Models {
		if err := checkCostPerToken(fmt.Sprintf("pricing model %q", model), "input_cost_per_token", price.InputCostPerToken); err != nil {
			return err
		}
		if err := checkCostPerToken(fmt.Sprintf("pricing model %q", model), "output_cost_per_token", price.OutputCostPerToken); err != nil {
			return err
		}
	}
	for _, backend := range backends {
		label := fmt.Sprintf("backend %q", backend.ID)
		if err := checkCostPerToken(label, "input_cost_per_token", backend.InputCostPerToken); err != nil {
			return err
		}
		if err := checkCostPerToken(label, "output_cost_per_token", backend.OutputCostPerToken); err != nil {
			return err
		}
	}
	return nil
}

func checkCostPerToken(owner string, field string, value float64) error {
	if value < 0 {
		return fmt.Errorf("%s %s cannot be negative", owner, field)
	}
	if value >= maxCostPerToken {
		return fmt.Errorf("%s %s is %g, which is too high for a per-token price; set the price for a single token", owner, field, value)
	}
	return nil
}

func (a *App) applyPricingTable() {
	for i := range a.Backends {
		price, ok := a.Pricing.Models[a.Backends[i].Model]
		if !ok {
			continue
		}
		if a.Backends[i].InputCostPerToken == 0 {
			a.Backends[i].InputCostPerToken = price.InputCostPerToken
		}
		if a.Backends[i].OutputCostPerToken == 0 {
			a.Backends[i].OutputCostPerToken = price.OutputCostPerToken
		}
	}
}

func validateRouter(name string) error {
	switch name {
	case "static", "round_robin", "round-robin", "least_loaded", "least-loaded", "ewma", "cost_aware", "cost-aware", "p2c", "litellm_shuffle", "litellm-shuffle", "envoy_least_request", "envoy-least-request", "bandit":
		return nil
	default:
		return fmt.Errorf("unsupported router %q", name)
	}
}

func validateFilters(filters []string) error {
	for _, name := range filters {
		switch name {
		case "health", "circuit", "concurrency", "tenant":
		default:
			return fmt.Errorf("unsupported filter %q", name)
		}
	}
	return nil
}

func defaultServerDuration(value Duration, fallback time.Duration, name string) (Duration, error) {
	if value.Duration < 0 {
		return Duration{}, fmt.Errorf("server %s cannot be negative", name)
	}
	if value.Duration == 0 {
		value.Duration = fallback
	}
	return value, nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			d.Duration = 0
			return nil
		}
		value, err := time.ParseDuration(text)
		if err != nil {
			return err
		}
		d.Duration = value
		return nil
	}

	var millis int64
	if err := json.Unmarshal(data, &millis); err != nil {
		return err
	}
	d.Duration = time.Duration(millis) * time.Millisecond
	return nil
}
