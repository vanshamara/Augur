package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/vanshamara/Augur/internal/backend"
	appconfig "github.com/vanshamara/Augur/internal/config"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/httpapi"
)

type explainFlags struct {
	ConfigPath          string
	RequestPath         string
	RequestID           string
	Prompt              string
	RequestType         string
	TenantID            string
	UserID              string
	UserTier            string
	LatencyBudgetMs     int
	CostBudgetUSD       float64
	PromptTokens        int
	MaxCompletionTokens int
}

type explainChatRequest struct {
	Messages            []explainMessage  `json:"messages"`
	Metadata            map[string]string `json:"metadata"`
	MaxCompletionTokens int               `json:"max_completion_tokens"`
	MaxTokens           int               `json:"max_tokens"`
	Temperature         *float64          `json:"temperature"`
}

type explainMessage struct {
	Role    string         `json:"role"`
	Content explainContent `json:"content"`
}

type explainContent struct {
	Text string
}

type explainOutput struct {
	ProviderCalled    bool                        `json:"provider_called"`
	RequestID         string                      `json:"request_id"`
	TenantID          string                      `json:"tenant_id,omitempty"`
	RouteName         string                      `json:"route_name,omitempty"`
	RequestType       core.RequestType            `json:"request_type,omitempty"`
	PromptTokens      int                         `json:"prompt_tokens"`
	LatencyBudgetMs   int                         `json:"latency_budget_ms,omitempty"`
	CostBudgetUSD     float64                     `json:"cost_budget_usd,omitempty"`
	Candidates        []core.BackendID            `json:"candidates"`
	Excluded          []dataplane.ExclusionRecord `json:"excluded,omitempty"`
	Canary            dataplane.CanaryRecord      `json:"canary"`
	Selected          core.BackendID              `json:"selected,omitempty"`
	FallbackPlan      []core.BackendID            `json:"fallback_plan,omitempty"`
	AttemptedBackends []core.BackendID            `json:"attempted_backends,omitempty"`
	FallbackCount     int                         `json:"fallback_count,omitempty"`
	EstimatedCostUSD  float64                     `json:"estimated_cost_usd,omitempty"`
	ReasonSummary     string                      `json:"reason_summary,omitempty"`
	Error             string                      `json:"error,omitempty"`
}

type dryBackend struct {
	id core.BackendID
}

func runExplain(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	return runExplainWithInput(ctx, args, getenv, os.Stdin, stdout)
}

func runExplainWithInput(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout io.Writer) error {
	options, err := parseExplainFlags(args, getenv)
	if err != nil {
		return err
	}
	config, err := appconfig.LoadFile(options.ConfigPath)
	if err != nil {
		return err
	}
	req, err := explainCoreRequest(options, config, stdin)
	if err != nil {
		return err
	}
	record, err := explainDecision(ctx, config, req)
	if err != nil && record.RequestID == "" {
		return err
	}
	out := explainOutputFrom(record, config)
	if err != nil {
		out.Error = err.Error()
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func parseExplainFlags(args []string, getenv func(string) string) (explainFlags, error) {
	flags := flag.NewFlagSet("explain", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options explainFlags
	flags.StringVar(&options.ConfigPath, "config", "", "config file path")
	flags.StringVar(&options.RequestPath, "request", "", "OpenAI-style request JSON path")
	flags.StringVar(&options.RequestID, "request-id", "req-explain", "request id")
	flags.StringVar(&options.Prompt, "prompt", "", "prompt text")
	flags.StringVar(&options.RequestType, "type", "", "request type")
	flags.StringVar(&options.TenantID, "tenant", "", "tenant id")
	flags.StringVar(&options.UserID, "user-id", "", "user id")
	flags.StringVar(&options.UserTier, "user-tier", "", "user tier")
	flags.IntVar(&options.LatencyBudgetMs, "latency-budget-ms", 0, "latency budget in milliseconds")
	flags.Float64Var(&options.CostBudgetUSD, "cost-budget-usd", 0, "cost budget in USD")
	flags.IntVar(&options.PromptTokens, "prompt-tokens", 0, "prompt token count")
	flags.IntVar(&options.MaxCompletionTokens, "max-completion-tokens", 0, "max completion tokens")
	if err := flags.Parse(args); err != nil {
		return explainFlags{}, err
	}
	if flags.NArg() != 0 {
		return explainFlags{}, fmt.Errorf("unexpected explain argument %q", flags.Arg(0))
	}
	options.ConfigPath = strings.TrimSpace(options.ConfigPath)
	if options.ConfigPath == "" {
		options.ConfigPath = strings.TrimSpace(getenv("AUGUR_CONFIG"))
	}
	if options.ConfigPath == "" {
		return explainFlags{}, errors.New("explain requires --config or AUGUR_CONFIG")
	}
	if strings.TrimSpace(options.RequestPath) == "" && strings.TrimSpace(options.Prompt) == "" {
		return explainFlags{}, errors.New("explain requires --request or --prompt")
	}
	return options, nil
}

func explainCoreRequest(options explainFlags, config appconfig.App, stdin io.Reader) (core.Request, error) {
	body, err := readExplainRequest(options, stdin)
	if err != nil {
		return core.Request{}, err
	}
	messages := explainMessages(body, options.Prompt)
	if len(messages) == 0 {
		return core.Request{}, errors.New("explain request must include at least one message or --prompt")
	}
	prompt := flattenExplainMessages(messages)
	promptTokens := options.PromptTokens
	if promptTokens == 0 {
		promptTokens = metadataInt(body.Metadata, "augur_prompt_tokens", "prompt_tokens")
	}
	if promptTokens == 0 {
		promptTokens = httpapi.EstimateTokens(prompt)
	}
	requestType, err := explainRequestType(options, body.Metadata, prompt, promptTokens)
	if err != nil {
		return core.Request{}, err
	}
	inferredDefaults := httpapi.InferRequestDefaults(prompt, requestType, promptTokens)
	tenantID := strings.TrimSpace(options.TenantID)
	if tenantID == "" {
		tenantID = config.Tenants.DefaultTenant
	}
	if tenantID == "" {
		tenantID = "default"
	}
	return core.Request{
		ID:                  defaultText(options.RequestID, "req-explain"),
		TenantID:            tenantID,
		UserID:              firstText(options.UserID, metadataText(body.Metadata, "augur_user_id", "user_id")),
		Prompt:              prompt,
		Messages:            messages,
		MaxCompletionTokens: explainMaxCompletionTokens(options, body, config),
		Temperature:         body.Temperature,
		Features: core.Features{
			PromptTokens:    promptTokens,
			Type:            requestType,
			LatencyBudgetMs: explainLatencyBudget(options, body, inferredDefaults, config),
			CostBudget:      explainCostBudget(options, body, inferredDefaults, config),
			UserTier:        firstText(options.UserTier, metadataText(body.Metadata, "augur_user_tier", "user_tier")),
		},
	}, nil
}

func readExplainRequest(options explainFlags, stdin io.Reader) (explainChatRequest, error) {
	source := strings.TrimSpace(options.RequestPath)
	if source == "" {
		return explainChatRequest{}, nil
	}
	data, err := explainRequestData(source, stdin)
	if err != nil {
		return explainChatRequest{}, err
	}
	var body explainChatRequest
	if err := json.Unmarshal(data, &body); err != nil {
		return explainChatRequest{}, err
	}
	return body, nil
}

func explainRequestData(source string, stdin io.Reader) ([]byte, error) {
	if source == "-" {
		return io.ReadAll(stdin)
	}
	if looksLikeJSON(source) {
		return []byte(source), nil
	}
	return os.ReadFile(source)
}

func looksLikeJSON(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")
}

func explainMessages(body explainChatRequest, prompt string) []core.Message {
	if strings.TrimSpace(prompt) != "" {
		return []core.Message{{Role: "user", Content: strings.TrimSpace(prompt)}}
	}
	messages := make([]core.Message, 0, len(body.Messages))
	for _, message := range body.Messages {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content.Text)
		if role == "" || content == "" {
			continue
		}
		messages = append(messages, core.Message{Role: role, Content: content})
	}
	return messages
}

func explainRequestType(options explainFlags, metadata map[string]string, prompt string, promptTokens int) (core.RequestType, error) {
	value := firstText(options.RequestType, metadataText(metadata, "augur_request_type", "request_type"))
	if value == "" {
		return httpapi.InferRequestType(prompt, promptTokens), nil
	}
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

func explainMaxCompletionTokens(options explainFlags, body explainChatRequest, config appconfig.App) int {
	if options.MaxCompletionTokens > 0 {
		return options.MaxCompletionTokens
	}
	if body.MaxCompletionTokens > 0 {
		return body.MaxCompletionTokens
	}
	if body.MaxTokens > 0 {
		return body.MaxTokens
	}
	return config.Budgets.MaxCompletionTokens
}

func explainLatencyBudget(options explainFlags, body explainChatRequest, inferred httpapi.RequestDefaults, config appconfig.App) int {
	if options.LatencyBudgetMs > 0 {
		return options.LatencyBudgetMs
	}
	if value := metadataInt(body.Metadata, "augur_latency_budget_ms", "latency_budget_ms"); value > 0 {
		return value
	}
	if inferred.LatencyBudgetMs > 0 {
		return inferred.LatencyBudgetMs
	}
	return config.Budgets.LatencyBudgetMs
}

func explainCostBudget(options explainFlags, body explainChatRequest, inferred httpapi.RequestDefaults, config appconfig.App) float64 {
	if options.CostBudgetUSD > 0 {
		return options.CostBudgetUSD
	}
	if value := metadataFloat(body.Metadata, "augur_cost_budget_usd", "cost_budget_usd"); value > 0 {
		return value
	}
	if inferred.CostBudgetUSD > 0 {
		return inferred.CostBudgetUSD
	}
	return config.Budgets.CostBudgetUSD
}

func explainDecision(ctx context.Context, config appconfig.App, req core.Request) (dataplane.RouteDecisionRecord, error) {
	ids := backendIDs(config.Backends)
	routing, err := buildRouter(config, ids)
	if err != nil {
		return dataplane.RouteDecisionRecord{}, err
	}
	defer closeGateway(routing.Router)
	filters, err := buildFilters(config, ids)
	if err != nil {
		return dataplane.RouteDecisionRecord{}, err
	}
	log := dataplane.NewDecisionLog(1)
	gateway, err := dataplane.New(dataplane.Config{
		Router:          routing.Router,
		Backends:        buildDryBackends(ids),
		Routes:          buildRouteRules(config.Routes),
		Capabilities:    buildBackendCapabilities(config.Backends),
		Canary:          buildCanaryConfig(config.Canary),
		Pricing:         buildBackendPricing(config.Backends),
		RequirePricing:  config.Budgets.RequirePricing,
		Decisions:       log,
		BackendTimeouts: buildBackendTimeouts(config.Backends),
		Filters:         filters,
		Hedge:           buildHedge(config.DataPlane.Hedge),
	})
	if err != nil {
		return dataplane.RouteDecisionRecord{}, err
	}
	_, callErr := gateway.Call(ctx, req)
	record, ok := log.Lookup(req.ID)
	if !ok {
		return dataplane.RouteDecisionRecord{}, errors.New("explain did not produce a decision record")
	}
	return record, callErr
}

func buildDryBackends(ids []core.BackendID) []backend.Backend {
	backends := make([]backend.Backend, 0, len(ids))
	for _, id := range ids {
		backends = append(backends, dryBackend{id: id})
	}
	return backends
}

func (b dryBackend) ID() core.BackendID {
	return b.id
}

func (b dryBackend) Call(ctx context.Context, req core.Request) (core.Response, error) {
	return core.Response{
		RequestID: req.ID,
		TenantID:  req.TenantID,
		Backend:   b.id,
	}, nil
}

func explainOutputFrom(record dataplane.RouteDecisionRecord, config appconfig.App) explainOutput {
	return explainOutput{
		ProviderCalled:    false,
		RequestID:         record.RequestID,
		TenantID:          record.TenantID,
		RouteName:         record.RouteName,
		RequestType:       record.RequestType,
		PromptTokens:      record.PromptTokens,
		LatencyBudgetMs:   record.LatencyBudgetMs,
		CostBudgetUSD:     record.CostBudgetUSD,
		Candidates:        record.Candidates,
		Excluded:          record.Excluded,
		Canary:            record.Canary,
		Selected:          record.Selected,
		FallbackPlan:      fallbackPlanForRoute(config.Routes, record.RouteName),
		AttemptedBackends: record.AttemptedBackends,
		FallbackCount:     record.FallbackCount,
		EstimatedCostUSD:  record.EstimatedCostUSD,
		ReasonSummary:     record.ReasonSummary,
		Error:             record.Error,
	}
}

func fallbackPlanForRoute(routes []appconfig.Route, routeName string) []core.BackendID {
	for _, route := range routes {
		if route.Name != routeName {
			continue
		}
		out := make([]core.BackendID, 0, len(route.Fallbacks))
		for _, fallback := range route.Fallbacks {
			out = append(out, fallback.Backend)
		}
		return out
	}
	return nil
}

func (c *explainContent) UnmarshalJSON(data []byte) error {
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
	var values []string
	for _, part := range parts {
		if part.Type == "" || part.Type == "text" {
			values = append(values, part.Text)
		}
	}
	c.Text = strings.Join(values, "\n")
	return nil
}

func flattenExplainMessages(messages []core.Message) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		lines = append(lines, fmt.Sprintf("%s: %s", message.Role, message.Content))
	}
	return strings.Join(lines, "\n")
}

func metadataText(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(metadata[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func metadataInt(metadata map[string]string, keys ...string) int {
	value, err := strconv.Atoi(metadataText(metadata, keys...))
	if err != nil {
		return 0
	}
	return value
}

func metadataFloat(metadata map[string]string, keys ...string) float64 {
	value, err := strconv.ParseFloat(metadataText(metadata, keys...), 64)
	if err != nil {
		return 0
	}
	return value
}

func firstText(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func defaultText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
