package anthropic

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/vanshamara/Augur/internal/anthropicapi"
	"github.com/vanshamara/Augur/internal/core"
)

const defaultMaxTokens = 1024

type Config struct {
	ID                  core.BackendID
	Model               string
	Client              *anthropicapi.Client
	HealthPath          string
	InputCostPerToken   float64
	OutputCostPerToken  float64
	MaxCompletionTokens int
}

type Backend struct {
	id                  core.BackendID
	model               string
	client              *anthropicapi.Client
	healthPath          string
	inputCostPerToken   float64
	outputCostPerToken  float64
	maxCompletionTokens int
}

func New(config Config) (*Backend, error) {
	if config.ID == "" {
		config.ID = core.BackendID(config.Model)
	}
	if config.ID == "" {
		return nil, errors.New("backend id is required")
	}
	if config.Model == "" {
		return nil, errors.New("model is required")
	}
	if config.Client == nil {
		return nil, errors.New("anthropic client is required")
	}
	return &Backend{
		id:                  config.ID,
		model:               config.Model,
		client:              config.Client,
		healthPath:          strings.TrimSpace(config.HealthPath),
		inputCostPerToken:   config.InputCostPerToken,
		outputCostPerToken:  config.OutputCostPerToken,
		maxCompletionTokens: config.MaxCompletionTokens,
	}, nil
}

func (b *Backend) ID() core.BackendID {
	return b.id
}

func (b *Backend) Check(ctx context.Context) error {
	return b.client.HealthCheck(ctx, b.healthPath)
}

// Call sends a chat request to the Anthropic Messages API. Anthropic has no
// embeddings API, so embedding requests are rejected for this backend.
func (b *Backend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	start := time.Now()
	if req.Features.Type == core.Embedding {
		return core.Response{RequestID: req.ID, Backend: b.id, Outcome: core.Outcome{LatencyMs: elapsedMs(start), Errored: true}},
			errors.New("anthropic backend does not support embeddings")
	}

	system, messages := splitMessages(req)
	result, err := b.client.Messages(ctx, anthropicapi.MessageRequest{
		Model:       b.model,
		MaxTokens:   b.maxTokens(req),
		System:      system,
		Messages:    messages,
		Temperature: req.Temperature,
	})
	if err != nil {
		return core.Response{RequestID: req.ID, Backend: b.id, Outcome: core.Outcome{LatencyMs: elapsedMs(start), Errored: true}}, err
	}

	cost := float64(result.InputTokens)*b.inputCostPerToken + float64(result.OutputTokens)*b.outputCostPerToken
	return core.Response{
		RequestID:  req.ID,
		Backend:    b.id,
		OutputText: result.Text,
		Outcome: core.Outcome{
			LatencyMs:    elapsedMs(start),
			CostUSD:      cost,
			OutputTokens: result.OutputTokens,
		},
	}, nil
}

func (b *Backend) maxTokens(req core.Request) int {
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	if b.maxCompletionTokens > 0 {
		return b.maxCompletionTokens
	}
	return defaultMaxTokens
}

// splitMessages moves system messages into the Anthropic top-level system field
// and keeps the rest as the conversation, since Anthropic only takes user and
// assistant roles in messages.
func splitMessages(req core.Request) (string, []anthropicapi.Message) {
	if len(req.Messages) == 0 {
		return "", []anthropicapi.Message{{Role: "user", Content: req.Prompt}}
	}
	var systemParts []string
	messages := make([]anthropicapi.Message, 0, len(req.Messages))
	for _, message := range req.Messages {
		if message.Role == "system" {
			systemParts = append(systemParts, message.Content)
			continue
		}
		messages = append(messages, anthropicapi.Message{Role: message.Role, Content: message.Content})
	}
	if len(messages) == 0 {
		messages = append(messages, anthropicapi.Message{Role: "user", Content: req.Prompt})
	}
	return strings.Join(systemParts, "\n"), messages
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
