package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
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

type ReadyFunc func(ctx context.Context) bool

type Config struct {
	Gateway      Gateway
	Now          func() time.Time
	NewID        func() string
	Ready        ReadyFunc
	AuthKeys     []string
	Defaults     RequestDefaults
	MaxBodyBytes int64
}

type RequestDefaults struct {
	LatencyBudgetMs     int
	CostBudgetUSD       float64
	MaxCompletionTokens int
	Temperature         *float64
}

type Server struct {
	gateway      Gateway
	now          func() time.Time
	newID        func() string
	ready        ReadyFunc
	authKeys     []string
	defaults     RequestDefaults
	maxBodyBytes int64
	mux          *http.ServeMux
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
	if config.Ready == nil {
		config.Ready = alwaysReady
	}
	if config.MaxBodyBytes < 0 {
		return nil, errors.New("max body bytes cannot be negative")
	}
	if config.MaxBodyBytes == 0 {
		config.MaxBodyBytes = 1_048_576
	}

	server := &Server{
		gateway:      config.Gateway,
		now:          config.Now,
		newID:        config.NewID,
		ready:        config.Ready,
		authKeys:     cleanAuthKeys(config.AuthKeys),
		defaults:     config.Defaults,
		maxBodyBytes: config.MaxBodyBytes,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealth)
	mux.HandleFunc("/readyz", server.handleReady)
	mux.HandleFunc("/v1/chat/completions", server.handleAuthenticatedChatCompletions)
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

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	if !s.ready(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleAuthenticatedChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid API key is required")
		return
	}
	s.handleChatCompletions(w, r)
}

func (s *Server) authorized(r *http.Request) bool {
	if len(s.authKeys) == 0 {
		return true
	}
	key := bearerToken(r.Header.Get("Authorization"))
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("X-Augur-API-Key"))
	}
	if key == "" {
		return false
	}
	for _, accepted := range s.authKeys {
		if subtle.ConstantTimeCompare([]byte(key), []byte(accepted)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}

	var body chatCompletionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBodyBytes))
	if err := decoder.Decode(&body); err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	req := body.coreRequest(requestID(r), s.newID(), s.defaults)
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

func (r chatCompletionRequest) coreRequest(id string, fallbackID string, defaults RequestDefaults) core.Request {
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
		MaxCompletionTokens: r.maxCompletionTokens(defaults.MaxCompletionTokens),
		Temperature:         r.temperature(defaults.Temperature),
		Features: core.Features{
			PromptTokens:    estimateTokens(prompt),
			Type:            core.Chat,
			LatencyBudgetMs: defaults.LatencyBudgetMs,
			CostBudget:      defaults.CostBudgetUSD,
		},
	}
}

func (r chatCompletionRequest) maxCompletionTokens(fallback int) int {
	if r.MaxCompletionTokens > 0 {
		return r.MaxCompletionTokens
	}
	if r.MaxTokens > 0 {
		return r.MaxTokens
	}
	return fallback
}

func (r chatCompletionRequest) temperature(fallback *float64) *float64 {
	if r.Temperature != nil {
		return r.Temperature
	}
	return fallback
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

func cleanAuthKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			out = append(out, key)
		}
	}
	return out
}

func bearerToken(value string) string {
	value = strings.TrimSpace(value)
	prefix := "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
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

func alwaysReady(ctx context.Context) bool {
	return true
}
