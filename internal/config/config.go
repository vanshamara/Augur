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
)

type App struct {
	Server    Server               `json:"server"`
	OpenAI    OpenAI               `json:"openai"`
	Backends  []Backend            `json:"backends"`
	Router    Router               `json:"router"`
	DataPlane DataPlane            `json:"data_plane"`
	Learning  Learning             `json:"learning"`
	Pricing   Pricing              `json:"pricing"`
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
	ID                  core.BackendID `json:"id"`
	Model               string         `json:"model"`
	InputCostPerToken   float64        `json:"input_cost_per_token"`
	OutputCostPerToken  float64        `json:"output_cost_per_token"`
	MaxCompletionTokens int            `json:"max_completion_tokens"`
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
	Health       map[core.BackendID]bool    `json:"health"`
	Prices       map[core.BackendID]float64 `json:"prices"`
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
	Enabled     bool     `json:"enabled"`
	Delay       Duration `json:"delay"`
	MaxInFlight int64    `json:"max_in_flight"`
}

type SingleFlight struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key"`
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
	}
	a.applyPricingTable()
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
		case "health", "circuit", "concurrency":
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
