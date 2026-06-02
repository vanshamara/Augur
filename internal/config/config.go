package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

const DefaultAddr = "127.0.0.1:8080"

type App struct {
	Server    Server               `json:"server"`
	OpenAI    OpenAI               `json:"openai"`
	Backends  []Backend            `json:"backends"`
	Router    Router               `json:"router"`
	DataPlane DataPlane            `json:"data_plane"`
	Learning  Learning             `json:"learning"`
	Policy    control.PolicyConfig `json:"policy"`
	Budgets   Budgets              `json:"budgets"`
}

type Server struct {
	Addr string `json:"addr"`
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
	Enabled        bool     `json:"enabled"`
	Tau            Duration `json:"tau"`
	PriorPrecision float64  `json:"prior_precision"`
	QueueSize      int      `json:"queue_size"`
	Judge          Judge    `json:"judge"`
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
	return Parse(data)
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
	if a.Learning.Judge.Enabled && strings.TrimSpace(a.Learning.Judge.Model) == "" {
		return App{}, errors.New("learning judge model is required when judge is enabled")
	}
	if a.Learning.Judge.Enabled && a.Policy.Exploration.JudgeSampleRate <= 0 {
		return App{}, errors.New("policy exploration judge_sample_rate must be positive when judge is enabled")
	}
	return a, nil
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
