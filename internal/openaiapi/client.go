package openaiapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openai.com/v1"
const defaultAPIKeyEnv = "OPENAI_API_KEY"

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

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

type JSONSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type ChatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Stream         bool            `json:"stream,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_completion_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type ChatCompletion struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
}

type ChatCompletionChunk struct {
	Content string
	Done    bool
}

type ChatCompletionStream struct {
	body   io.Closer
	reader *bufio.Reader
	done   bool
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("openai api status %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("openai api status %d", e.Status)
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

// ChatCompletion sends one chat completion request.
func (c *Client) ChatCompletion(ctx context.Context, body ChatCompletionRequest) (ChatCompletion, error) {
	if body.Model == "" {
		return ChatCompletion{}, errors.New("model is required")
	}
	if len(body.Messages) == 0 {
		return ChatCompletion{}, errors.New("messages are required")
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return ChatCompletion{}, err
	}

	endpoint := c.baseURL.JoinPath("chat", "completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return ChatCompletion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return ChatCompletion{}, err
	}
	defer resp.Body.Close()

	var decoded chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return ChatCompletion{}, &APIError{Status: resp.StatusCode}
		}
		return ChatCompletion{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatCompletion{}, decoded.errorValue(resp.StatusCode)
	}
	if len(decoded.Choices) == 0 {
		return ChatCompletion{}, errors.New("chat completion returned no choices")
	}

	return ChatCompletion{
		Content:          decoded.Choices[0].Message.Content,
		PromptTokens:     decoded.Usage.PromptTokens,
		CompletionTokens: decoded.Usage.CompletionTokens,
	}, nil
}

func (c *Client) ChatCompletionStream(ctx context.Context, body ChatCompletionRequest) (*ChatCompletionStream, error) {
	if body.Model == "" {
		return nil, errors.New("model is required")
	}
	if len(body.Messages) == 0 {
		return nil, errors.New("messages are required")
	}
	body.Stream = true

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := c.baseURL.JoinPath("chat", "completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var decoded chatResponse
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return nil, &APIError{Status: resp.StatusCode}
		}
		return nil, decoded.errorValue(resp.StatusCode)
	}
	return &ChatCompletionStream{body: resp.Body, reader: bufio.NewReader(resp.Body)}, nil
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
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

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

func (s *ChatCompletionStream) Recv() (ChatCompletionChunk, error) {
	if s.done {
		return ChatCompletionChunk{}, io.EOF
	}
	for {
		line, err := s.reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			chunk, chunkErr := parseStreamData(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			if chunkErr != nil {
				return ChatCompletionChunk{}, chunkErr
			}
			if chunk.Done {
				s.done = true
			}
			return chunk, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.done = true
			}
			return ChatCompletionChunk{}, err
		}
	}
}

func (s *ChatCompletionStream) Close() error {
	s.done = true
	return s.body.Close()
}

func parseStreamData(data string) (ChatCompletionChunk, error) {
	if data == "[DONE]" {
		return ChatCompletionChunk{Done: true}, nil
	}
	var decoded chatStreamResponse
	if err := json.Unmarshal([]byte(data), &decoded); err != nil {
		return ChatCompletionChunk{}, err
	}
	if len(decoded.Choices) == 0 {
		return ChatCompletionChunk{}, nil
	}
	return ChatCompletionChunk{Content: decoded.Choices[0].Delta.Content}, nil
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type chatStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func (r chatResponse) errorValue(status int) error {
	if r.Error != nil && r.Error.Message != "" {
		return &APIError{Status: status, Message: r.Error.Message}
	}
	return &APIError{Status: status}
}
