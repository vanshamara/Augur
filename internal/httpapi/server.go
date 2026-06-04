package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
)

type Gateway interface {
	Call(ctx context.Context, req core.Request) (core.Response, error)
}

type StreamingGateway interface {
	Stream(ctx context.Context, req core.Request) (core.Stream, error)
}

type ReadyFunc func(ctx context.Context) bool

type Config struct {
	Gateway        Gateway
	Now            func() time.Time
	NewID          func() string
	Ready          ReadyFunc
	AuthKeys       []string
	Defaults       RequestDefaults
	TenantHeader   string
	DefaultTenant  string
	TenantDefaults map[string]RequestDefaults
	MaxBodyBytes   int64
}

type RequestDefaults struct {
	LatencyBudgetMs     int
	CostBudgetUSD       float64
	MaxCompletionTokens int
	Temperature         *float64
	UserTier            string
}

type requestOptions struct {
	RequestType     core.RequestType
	LatencyBudgetMs int
	CostBudgetUSD   float64
	UserTier        string
}

type Server struct {
	gateway        Gateway
	now            func() time.Time
	newID          func() string
	ready          ReadyFunc
	authKeys       []string
	defaults       RequestDefaults
	tenantHeader   string
	defaultTenant  string
	tenantDefaults map[string]RequestDefaults
	maxBodyBytes   int64
	mux            *http.ServeMux
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
	config.TenantHeader = strings.TrimSpace(config.TenantHeader)
	if config.TenantHeader == "" {
		config.TenantHeader = "X-Augur-Tenant"
	}
	config.DefaultTenant = strings.TrimSpace(config.DefaultTenant)
	if config.DefaultTenant == "" {
		config.DefaultTenant = "default"
	}

	server := &Server{
		gateway:        config.Gateway,
		now:            config.Now,
		newID:          config.NewID,
		ready:          config.Ready,
		authKeys:       cleanAuthKeys(config.AuthKeys),
		defaults:       config.Defaults,
		tenantHeader:   config.TenantHeader,
		defaultTenant:  config.DefaultTenant,
		tenantDefaults: cleanTenantDefaults(config.TenantDefaults),
		maxBodyBytes:   config.MaxBodyBytes,
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
	options, err := requestOptionsFrom(r, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if body.Stream {
		s.handleChatCompletionStream(w, r, body, options)
		return
	}

	tenantID := s.tenantID(r)
	req := body.coreRequest(requestID(r), s.newID(), tenantID, s.defaultsForTenant(tenantID), options)
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

func (s *Server) handleChatCompletionStream(w http.ResponseWriter, r *http.Request, body chatCompletionRequest, options requestOptions) {
	gateway, ok := s.gateway.(StreamingGateway)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "streaming is not supported by this gateway")
		return
	}

	tenantID := s.tenantID(r)
	req := body.coreRequest(requestID(r), s.newID(), tenantID, s.defaultsForTenant(tenantID), options)
	stream, err := gateway.Stream(r.Context(), req)
	if err != nil {
		writeGatewayError(w, err)
		return
	}
	defer stream.Close()

	streamID := s.newID()
	created := s.now().Unix()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flush(w)

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				writeStreamData(w, errorResponse{Error: errorBody{Message: err.Error(), Type: "upstream_error"}})
				flush(w)
			}
			return
		}
		if chunk.Delta != "" {
			writeStreamData(w, body.streamDelta(streamID, created, chunk.Delta))
			flush(w)
		}
		if chunk.Done {
			writeStreamData(w, body.streamDone(streamID, created))
			w.Write([]byte("data: [DONE]\n\n"))
			flush(w)
			return
		}
	}
}

type chatCompletionRequest struct {
	Model               string            `json:"model"`
	Messages            []chatMessage     `json:"messages"`
	Metadata            map[string]string `json:"metadata"`
	Stream              bool              `json:"stream"`
	N                   *int              `json:"n"`
	MaxCompletionTokens int               `json:"max_completion_tokens"`
	MaxTokens           int               `json:"max_tokens"`
	Temperature         *float64          `json:"temperature"`
}

func (r chatCompletionRequest) validate() error {
	if r.Model == "" {
		return errors.New("model is required")
	}
	if len(r.Messages) == 0 {
		return errors.New("messages are required")
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

func (r chatCompletionRequest) coreRequest(id string, fallbackID string, tenantID string, defaults RequestDefaults, options requestOptions) core.Request {
	if id == "" {
		id = fallbackID
	}
	messages := make([]core.Message, len(r.Messages))
	for i, msg := range r.Messages {
		messages[i] = core.Message{Role: msg.Role, Content: msg.Content.Text}
	}
	prompt := flattenMessages(messages)
	defaults = defaults.withOptions(options)
	requestType := options.RequestType
	if requestType == "" {
		requestType = core.Chat
	}
	return core.Request{
		ID:                  id,
		TenantID:            tenantID,
		Prompt:              prompt,
		Messages:            messages,
		MaxCompletionTokens: r.maxCompletionTokens(defaults.MaxCompletionTokens),
		Temperature:         r.temperature(defaults.Temperature),
		Features: core.Features{
			PromptTokens:    estimateTokens(prompt),
			Type:            requestType,
			LatencyBudgetMs: defaults.LatencyBudgetMs,
			CostBudget:      defaults.CostBudgetUSD,
			UserTier:        defaults.UserTier,
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

func (r chatCompletionRequest) streamDelta(id string, created int64, content string) chatCompletionChunkResponse {
	return chatCompletionChunkResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   r.Model,
		Choices: []chatStreamChoice{
			{
				Index: 0,
				Delta: chatDeltaMessage{
					Content: content,
				},
			},
		},
	}
}

func (r chatCompletionRequest) streamDone(id string, created int64) chatCompletionChunkResponse {
	reason := "stop"
	return chatCompletionChunkResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   r.Model,
		Choices: []chatStreamChoice{
			{
				Index:        0,
				Delta:        chatDeltaMessage{},
				FinishReason: &reason,
			},
		},
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

type chatCompletionChunkResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []chatStreamChoice `json:"choices"`
}

type chatStreamChoice struct {
	Index        int              `json:"index"`
	Delta        chatDeltaMessage `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}

type chatDeltaMessage struct {
	Content string `json:"content,omitempty"`
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

func cleanTenantDefaults(defaults map[string]RequestDefaults) map[string]RequestDefaults {
	out := make(map[string]RequestDefaults, len(defaults))
	for tenant, value := range defaults {
		tenant = strings.TrimSpace(tenant)
		if tenant != "" && !value.Empty() {
			out[tenant] = value
		}
	}
	return out
}

func (s *Server) tenantID(r *http.Request) string {
	tenant := strings.TrimSpace(r.Header.Get(s.tenantHeader))
	if tenant == "" {
		return s.defaultTenant
	}
	return tenant
}

func (s *Server) defaultsForTenant(tenant string) RequestDefaults {
	defaults := s.defaults
	override, ok := s.tenantDefaults[tenant]
	if !ok {
		return defaults
	}
	return defaults.withOverride(override)
}

func (d RequestDefaults) Empty() bool {
	return d.LatencyBudgetMs == 0 &&
		d.CostBudgetUSD == 0 &&
		d.MaxCompletionTokens == 0 &&
		d.Temperature == nil &&
		d.UserTier == ""
}

func (d RequestDefaults) withOverride(override RequestDefaults) RequestDefaults {
	if override.LatencyBudgetMs > 0 {
		d.LatencyBudgetMs = override.LatencyBudgetMs
	}
	if override.CostBudgetUSD > 0 {
		d.CostBudgetUSD = override.CostBudgetUSD
	}
	if override.MaxCompletionTokens > 0 {
		d.MaxCompletionTokens = override.MaxCompletionTokens
	}
	if override.Temperature != nil {
		d.Temperature = override.Temperature
	}
	if override.UserTier != "" {
		d.UserTier = override.UserTier
	}
	return d
}

func (d RequestDefaults) withOptions(options requestOptions) RequestDefaults {
	if options.LatencyBudgetMs > 0 {
		d.LatencyBudgetMs = options.LatencyBudgetMs
	}
	if options.CostBudgetUSD > 0 {
		d.CostBudgetUSD = options.CostBudgetUSD
	}
	if options.UserTier != "" {
		d.UserTier = options.UserTier
	}
	return d
}

func requestOptionsFrom(r *http.Request, body chatCompletionRequest) (requestOptions, error) {
	options, err := requestOptionsFromMetadata(body.Metadata)
	if err != nil {
		return requestOptions{}, err
	}
	if err := options.applyHeaders(r.Header); err != nil {
		return requestOptions{}, err
	}
	options.applyInference(inferRequestOptions(body, options.RequestType))
	return options, nil
}

func requestOptionsFromMetadata(metadata map[string]string) (requestOptions, error) {
	var options requestOptions
	var err error
	if value := metadataValue(metadata, "augur_request_type", "request_type"); value != "" {
		options.RequestType, err = parseRequestType(value)
		if err != nil {
			return requestOptions{}, err
		}
	}
	if value := metadataValue(metadata, "augur_user_tier", "user_tier"); value != "" {
		options.UserTier = strings.TrimSpace(value)
	}
	if value := metadataValue(metadata, "augur_latency_budget_ms", "latency_budget_ms"); value != "" {
		options.LatencyBudgetMs, err = parsePositiveInt(value, "latency budget")
		if err != nil {
			return requestOptions{}, err
		}
	}
	if value := metadataValue(metadata, "augur_cost_budget_usd", "cost_budget_usd"); value != "" {
		options.CostBudgetUSD, err = parsePositiveFloat(value, "cost budget")
		if err != nil {
			return requestOptions{}, err
		}
	}
	return options, nil
}

func (o *requestOptions) applyHeaders(headers http.Header) error {
	var err error
	if value := headers.Get("X-Augur-Request-Type"); value != "" {
		o.RequestType, err = parseRequestType(value)
		if err != nil {
			return err
		}
	}
	if value := headers.Get("X-Augur-User-Tier"); value != "" {
		o.UserTier = strings.TrimSpace(value)
	}
	if value := headers.Get("X-Augur-Latency-Budget-Ms"); value != "" {
		o.LatencyBudgetMs, err = parsePositiveInt(value, "latency budget")
		if err != nil {
			return err
		}
	}
	if value := headers.Get("X-Augur-Cost-Budget-USD"); value != "" {
		o.CostBudgetUSD, err = parsePositiveFloat(value, "cost budget")
		if err != nil {
			return err
		}
	}
	return nil
}

func (o *requestOptions) applyInference(inferred requestOptions) {
	if o.RequestType == "" {
		o.RequestType = inferred.RequestType
	}
	if o.LatencyBudgetMs == 0 {
		o.LatencyBudgetMs = inferred.LatencyBudgetMs
	}
	if o.CostBudgetUSD == 0 {
		o.CostBudgetUSD = inferred.CostBudgetUSD
	}
}

func metadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func parseRequestType(value string) (core.RequestType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(core.Chat):
		return core.Chat, nil
	case string(core.Reasoning):
		return core.Reasoning, nil
	case string(core.Coding):
		return core.Coding, nil
	case string(core.Embedding):
		return core.Embedding, nil
	default:
		return "", fmt.Errorf("unsupported request type %q", value)
	}
}

func parsePositiveInt(value string, name string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func parsePositiveFloat(value string, name string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive number", name)
	}
	return parsed, nil
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
	case errors.Is(err, dataplane.ErrStreaming):
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
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

func writeStreamData(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	w.Write([]byte("data: "))
	w.Write(data)
	w.Write([]byte("\n\n"))
}

func flush(w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if ok {
		flusher.Flush()
	}
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
