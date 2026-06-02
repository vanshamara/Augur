package openai

import (
	"context"
	"errors"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/openaiapi"
)

type Config struct {
	ID                  core.BackendID
	Model               string
	Client              *openaiapi.Client
	InputCostPerToken   float64
	OutputCostPerToken  float64
	MaxCompletionTokens int
}

type Backend struct {
	id                  core.BackendID
	model               string
	client              *openaiapi.Client
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
		return nil, errors.New("openai client is required")
	}
	return &Backend{
		id:                  config.ID,
		model:               config.Model,
		client:              config.Client,
		inputCostPerToken:   config.InputCostPerToken,
		outputCostPerToken:  config.OutputCostPerToken,
		maxCompletionTokens: config.MaxCompletionTokens,
	}, nil
}

func (b *Backend) ID() core.BackendID {
	return b.id
}

func (b *Backend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	start := time.Now()
	result, err := b.client.ChatCompletion(ctx, openaiapi.ChatCompletionRequest{
		Model:       b.model,
		Messages:    chatMessages(req),
		Temperature: req.Temperature,
		MaxTokens:   maxCompletionTokens(req, b.maxCompletionTokens),
	})
	if err != nil {
		return core.Response{RequestID: req.ID, Backend: b.id, Outcome: core.Outcome{LatencyMs: elapsedMs(start), Errored: true}}, err
	}

	cost := float64(result.PromptTokens)*b.inputCostPerToken + float64(result.CompletionTokens)*b.outputCostPerToken
	return core.Response{
		RequestID:  req.ID,
		Backend:    b.id,
		OutputText: result.Content,
		Outcome: core.Outcome{
			LatencyMs:    elapsedMs(start),
			CostUSD:      cost,
			OutputTokens: result.CompletionTokens,
		},
	}, nil
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func chatMessages(req core.Request) []openaiapi.ChatMessage {
	if len(req.Messages) == 0 {
		return []openaiapi.ChatMessage{{Role: "user", Content: req.Prompt}}
	}
	messages := make([]openaiapi.ChatMessage, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = openaiapi.ChatMessage{Role: msg.Role, Content: msg.Content}
	}
	return messages
}

func maxCompletionTokens(req core.Request, fallback int) int {
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	return fallback
}
