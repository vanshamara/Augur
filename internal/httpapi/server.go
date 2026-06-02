package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
)

type Gateway interface {
	Call(ctx context.Context, req core.Request) (core.Response, error)
}

type Config struct {
	Gateway Gateway
	Now     func() time.Time
	NewID   func() string
}

type Server struct {
	gateway Gateway
	now     func() time.Time
	newID   func() string
	mux     *http.ServeMux
}

func New(config Config) (*Server, error) {
	if config.Gateway == nil {
		return nil, errors.New("gateway is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = randomCompletionID
	}

	server := &Server{gateway: config.Gateway, now: config.Now, newID: config.NewID}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealth)
	mux.HandleFunc("/v1/chat/completions", server.handleChatCompletions)
	server.mux = mux
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}

	var body chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	req := body.coreRequest(requestID(r), s.newID())
	resp, err := s.gateway.Call(r.Context(), req)
	if err != nil {
		writeGatewayError(w, err)
		return
	}
	if resp.Errored {
		writeError(w, http.StatusBadGateway, "upstream_error", "backend returned an errored response")
		return
	}

	w.Header().Set("X-Augur-Backend", string(resp.Backend))
	writeJSON(w, http.StatusOK, body.response(req, resp, s.newID(), s.now()))
}

type chatCompletionRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	Stream              bool          `json:"stream"`
	N                   *int          `json:"n"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
	MaxTokens           int           `json:"max_tokens"`
	Temperature         *float64      `json:"temperature"`
}

func (r chatCompletionRequest) validate() error {
	if r.Model == "" {
		return errors.New("model is required")
	}
	if len(r.Messages) == 0 {
		return errors.New("messages are required")
	}
	if r.Stream {
		return errors.New("streaming is not supported yet")
	}
	if r.N != nil && *r.N != 1 {
		return errors.New("n must be 1")
	}
	for _, msg := range r.Messages {
		if msg.Role == "" {
			return errors.New("message role is required")
		}
		if msg.Content.Text == "" {
			return errors.New("message content is required")
		}
	}
	return nil
}

func (r chatCompletionRequest) coreRequest(id string, fallbackID string) core.Request {
	if id == "" {
		id = fallbackID
	}
	messages := make([]core.Message, len(r.Messages))
	for i, msg := range r.Messages {
		messages[i] = core.Message{Role: msg.Role, Content: msg.Content.Text}
	}
	prompt := flattenMessages(messages)
	return core.Request{
		ID:                  id,
		Prompt:              prompt,
		Messages:            messages,
		MaxCompletionTokens: r.maxCompletionTokens(),
		Temperature:         r.Temperature,
		Features: core.Features{
			PromptTokens: estimateTokens(prompt),
			Type:         core.Chat,
		},
	}
}

func (r chatCompletionRequest) maxCompletionTokens() int {
	if r.MaxCompletionTokens > 0 {
		return r.MaxCompletionTokens
	}
	return r.MaxTokens
}

func (r chatCompletionRequest) response(req core.Request, resp core.Response, id string, now time.Time) chatCompletionResponse {
	completionTokens := resp.OutputTokens
	usage := chatUsage{
		PromptTokens:     req.Features.PromptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      req.Features.PromptTokens + completionTokens,
	}
	return chatCompletionResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: now.Unix(),
		Model:   r.Model,
		Choices: []chatChoice{
			{
				Index: 0,
				Message: chatResponseMessage{
					Role:    "assistant",
					Content: resp.OutputText,
				},
				FinishReason: "stop",
			},
		},
		Usage: usage,
	}
}

type chatMessage struct {
	Role    string         `json:"role"`
	Content messageContent `json:"content"`
}

type messageContent struct {
	Text string
}

func (c *messageContent) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		c.Text = text
		return nil
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &parts); err != nil {
		return errors.New("message content must be text")
	}
	var textParts []string
	for _, part := range parts {
		if part.Type == "" || part.Type == "text" {
			textParts = append(textParts, part.Text)
		}
	}
	c.Text = strings.Join(textParts, "\n")
	return nil
}

type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int                 `json:"index"`
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type chatResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func flattenMessages(messages []core.Message) string {
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		lines = append(lines, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
	}
	return strings.Join(lines, "\n")
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	tokens := len([]rune(text)) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	if id := r.Header.Get("OpenAI-Request-ID"); id != "" {
		return id
	}
	return ""
}

func writeGatewayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dataplane.ErrLoadShed):
		writeError(w, http.StatusTooManyRequests, "rate_limit_error", err.Error())
	case errors.Is(err, dataplane.ErrNoCandidates), errors.Is(err, dataplane.ErrMissing):
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", err.Error())
	default:
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, kind string, message string) {
	writeJSON(w, status, errorResponse{Error: errorBody{Message: message, Type: kind}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func randomCompletionID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(bytes[:])
}
