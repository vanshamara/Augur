package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/openaiapi"
	"github.com/vanshamara/Augur/internal/rng"
)

type JudgeConfig struct {
	Model      string
	Client     *openaiapi.Client
	SampleRate float64
	Seed       uint64
}

type JudgeScorer struct {
	model      string
	client     *openaiapi.Client
	sampleRate float64
	seed       uint64
}

func NewJudgeScorer(config JudgeConfig) (*JudgeScorer, error) {
	if config.Model == "" {
		return nil, errors.New("judge model is required")
	}
	if config.Client == nil {
		return nil, errors.New("openai client is required")
	}
	return &JudgeScorer{
		model:      config.Model,
		client:     config.Client,
		sampleRate: config.SampleRate,
		seed:       config.Seed,
	}, nil
}

func (j *JudgeScorer) ShouldScore(req core.Request, resp core.Response) bool {
	if j.sampleRate <= 0 {
		return false
	}
	if j.sampleRate >= 1 {
		return true
	}
	gen := rng.NewDeriver(j.seed).Rand(rng.HashKey(req.ID), rng.HashKey(string(resp.Backend)), rng.HashKey("judge"))
	return gen.Float64() < j.sampleRate
}

func (j *JudgeScorer) Score(ctx context.Context, req core.Request, resp core.Response) (Result, error) {
	result, err := j.client.ChatCompletion(ctx, openaiapi.ChatCompletionRequest{
		Model: j.model,
		Messages: []openaiapi.ChatMessage{
			{Role: "system", Content: judgeInstructions},
			{Role: "user", Content: judgePrompt(req, resp)},
		},
		ResponseFormat: judgeResponseFormat(),
	})
	if err != nil {
		return Result{}, err
	}

	var parsed judgeResult
	if err := json.Unmarshal([]byte(result.Content), &parsed); err != nil {
		return Result{}, err
	}
	return Result{Score: clamp01(parsed.Score), Reason: parsed.Reason}, nil
}

type judgeResult struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

const judgeInstructions = "Score answer quality from 0 to 1. Return only JSON with score and reason."

func judgePrompt(req core.Request, resp core.Response) string {
	return fmt.Sprintf("Request type: %s\nPrompt:\n%s\n\nAnswer:\n%s", req.Features.Type, req.Prompt, resp.OutputText)
}

func judgeResponseFormat() *openaiapi.ResponseFormat {
	return &openaiapi.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &openaiapi.JSONSchema{
			Name:   "quality_score",
			Strict: true,
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"score": map[string]any{
						"type":    "number",
						"minimum": 0,
						"maximum": 1,
					},
					"reason": map[string]any{
						"type": "string",
					},
				},
				"required": []string{"score", "reason"},
			},
		},
	}
}
