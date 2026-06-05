package anthropicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	defaultAPIKeyEnv = "ANTHROPIC_API_KEY"
	apiVersion       = "2023-06-01"
)

type Config struct {
	BaseURL   string
	APIKey    string
	APIKeyEnv string
	Client    *http.Client
}

type Client struct {
	baseURL *url.URL
	apiKey  string
	client  *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type MessageRequest struct {
	Model       string
	MaxTokens   int
	System      string
	Messages    []Message
	Temperature *float64
}

type MessageResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("anthropic api status %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("anthropic api status %d", e.Status)
}

func (e *APIError) StatusCode() int {
	return e.Status
}

func New(config Config) (*Client, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.APIKeyEnv == "" {
		config.APIKeyEnv = defaultAPIKeyEnv
	}
	if config.APIKey == "" {
		config.APIKey = os.Getenv(config.APIKeyEnv)
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("%s is not set", config.APIKeyEnv)
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 60 * time.Second}
	}

	baseURL, err := url.Parse(strings.TrimRight(config.BaseURL, "/"))
	if err != nil {
		return nil, err
	}
	return &Client{baseURL: baseURL, apiKey: config.APIKey, client: config.Client}, nil
}

type messagePayload struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	System      string    `json:"system,omitempty"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
}

// Messages sends one Anthropic Messages request and returns the text output and
// token usage.
func (c *Client) Messages(ctx context.Context, body MessageRequest) (MessageResult, error) {
	if body.Model == "" {
		return MessageResult{}, errors.New("model is required")
	}
	if len(body.Messages) == 0 {
		return MessageResult{}, errors.New("messages are required")
	}
	if body.MaxTokens <= 0 {
		return MessageResult{}, errors.New("max_tokens is required")
	}

	payload, err := json.Marshal(messagePayload{
		Model:       body.Model,
		MaxTokens:   body.MaxTokens,
		System:      body.System,
		Messages:    body.Messages,
		Temperature: body.Temperature,
	})
	if err != nil {
		return MessageResult{}, err
	}

	endpoint := c.baseURL.JoinPath("v1", "messages")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return MessageResult{}, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return MessageResult{}, err
	}
	defer resp.Body.Close()

	var decoded messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return MessageResult{}, &APIError{Status: resp.StatusCode}
		}
		return MessageResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MessageResult{}, decoded.errorValue(resp.StatusCode)
	}

	return MessageResult{
		Text:         decoded.text(),
		InputTokens:  decoded.Usage.InputTokens,
		OutputTokens: decoded.Usage.OutputTokens,
	}, nil
}

func (c *Client) HealthCheck(ctx context.Context, path string) error {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return nil
	}

	endpoint := c.baseURL.JoinPath(strings.Split(path, "/")...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode}
	}
	return nil
}

type messageResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r messageResponse) text() string {
	var parts []string
	for _, block := range r.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

func (r messageResponse) errorValue(status int) error {
	if r.Error != nil && r.Error.Message != "" {
		return &APIError{Status: status, Message: r.Error.Message}
	}
	return &APIError{Status: status}
}
