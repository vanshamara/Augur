package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/vanshamara/Augur/internal/core"
)

type embeddingsRequest struct {
	Model          string            `json:"model"`
	Input          embeddingInput    `json:"input"`
	Dimensions     *int              `json:"dimensions"`
	EncodingFormat string            `json:"encoding_format"`
	User           string            `json:"user"`
	Metadata       map[string]string `json:"metadata"`
}

type embeddingInput struct {
	Values []string
}

func (e *embeddingInput) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		e.Values = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("input must be a string or an array of strings")
	}
	e.Values = many
	return nil
}

func (r embeddingsRequest) validate() error {
	if r.Model == "" {
		return errors.New("model is required")
	}
	if len(r.Input.Values) == 0 {
		return errors.New("input is required")
	}
	for _, value := range r.Input.Values {
		if strings.TrimSpace(value) == "" {
			return errors.New("input values cannot be empty")
		}
	}
	if r.EncodingFormat != "" && r.EncodingFormat != "float" {
		return errors.New("only the float encoding_format is supported")
	}
	return nil
}

func (s *Server) handleAuthenticatedEmbeddings(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid API key is required")
		return
	}
	if !s.allowRequest(s.tenantID(r)) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "rate_limit_error", "tenant rate limit exceeded")
		return
	}
	s.handleEmbeddings(w, r)
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}

	var body embeddingsRequest
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
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "request body must contain one JSON object")
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	options, err := embeddingsOptions(r, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
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

	writeResponseRoutingHeaders(w, resp)
	writeJSON(w, http.StatusOK, body.response(req, resp))
}

func embeddingsOptions(r *http.Request, body embeddingsRequest) (requestOptions, error) {
	options, err := requestOptionsFromMetadata(body.Metadata)
	if err != nil {
		return requestOptions{}, err
	}
	if err := options.applyHeaders(r.Header); err != nil {
		return requestOptions{}, err
	}
	if options.UserID == "" {
		options.UserID = strings.TrimSpace(body.User)
	}
	options.RequestType = core.Embedding
	return options, nil
}

func (r embeddingsRequest) coreRequest(id string, fallbackID string, tenantID string, defaults RequestDefaults, options requestOptions) core.Request {
	if id == "" {
		id = fallbackID
	}
	inputs := make([]string, len(r.Input.Values))
	copy(inputs, r.Input.Values)
	joined := strings.Join(inputs, "\n")
	defaults = defaults.withOptions(options)
	promptTokens := options.PromptTokens
	if promptTokens == 0 {
		promptTokens = estimateTokens(joined)
	}
	return core.Request{
		ID:                  id,
		TenantID:            tenantID,
		UserID:              options.UserID,
		Prompt:              joined,
		Inputs:              inputs,
		EmbeddingDimensions: r.Dimensions,
		EmbeddingFormat:     strings.TrimSpace(r.EncodingFormat),
		Features: core.Features{
			PromptTokens:    promptTokens,
			Type:            core.Embedding,
			LatencyBudgetMs: defaults.LatencyBudgetMs,
			CostBudget:      defaults.CostBudgetUSD,
			UserTier:        defaults.UserTier,
		},
	}
}

func (r embeddingsRequest) response(req core.Request, resp core.Response) embeddingsResponse {
	data := make([]embeddingData, 0, len(resp.Embeddings))
	for index, vector := range resp.Embeddings {
		data = append(data, embeddingData{
			Object:    "embedding",
			Index:     index,
			Embedding: vector,
		})
	}
	return embeddingsResponse{
		Object: "list",
		Data:   data,
		Model:  r.Model,
		Usage: embeddingUsage{
			PromptTokens: req.Features.PromptTokens,
			TotalTokens:  req.Features.PromptTokens,
		},
	}
}

type embeddingsResponse struct {
	Object string          `json:"object"`
	Data   []embeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  embeddingUsage  `json:"usage"`
}

type embeddingData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type embeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}
