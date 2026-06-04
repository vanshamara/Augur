package httpapi

import (
	"strings"
	"unicode"

	"github.com/vanshamara/Augur/internal/core"
)

const (
	simpleLatencyBudgetMs    = 1000
	simpleCostBudgetUSD      = 0.002
	reasoningLatencyBudgetMs = 3000
	reasoningCostBudgetUSD   = 0.05
	codingLatencyBudgetMs    = 2400
	codingCostBudgetUSD      = 0.03
)

func inferRequestOptions(body chatCompletionRequest, explicitType core.RequestType, promptTokens int) requestOptions {
	prompt := body.promptText()
	tokens := promptTokens
	if tokens == 0 {
		tokens = estimateTokens(prompt)
	}
	requestType := explicitType
	if requestType == "" {
		requestType = inferRequestType(prompt, tokens)
	}

	options := requestOptions{RequestType: requestType}
	switch requestType {
	case core.Reasoning:
		options.LatencyBudgetMs = reasoningLatencyBudgetMs
		options.CostBudgetUSD = reasoningCostBudgetUSD
	case core.Coding:
		options.LatencyBudgetMs = codingLatencyBudgetMs
		options.CostBudgetUSD = codingCostBudgetUSD
	default:
		if simpleOrSpamPrompt(prompt, tokens) {
			options.LatencyBudgetMs = simpleLatencyBudgetMs
			options.CostBudgetUSD = simpleCostBudgetUSD
		}
	}
	return options
}

func (r chatCompletionRequest) promptText() string {
	messages := make([]core.Message, len(r.Messages))
	for i, msg := range r.Messages {
		messages[i] = core.Message{Role: msg.Role, Content: msg.Content.Text}
	}
	return flattenMessages(messages)
}

func inferRequestType(prompt string, tokens int) core.RequestType {
	normalized := normalizePrompt(prompt)
	if codingPrompt(normalized) {
		return core.Coding
	}
	if reasoningPrompt(normalized, tokens) {
		return core.Reasoning
	}
	return core.Chat
}

func codingPrompt(prompt string) bool {
	return containsAny(prompt, []string{
		"write code",
		"write a function",
		"implement",
		"debug",
		"stack trace",
		"compile error",
		"unit test",
		"typescript",
		"javascript",
		"python",
		"golang",
		"dockerfile",
		"kubernetes",
		"sql query",
	})
}

func reasoningPrompt(prompt string, tokens int) bool {
	if tokens >= 600 {
		return true
	}
	if containsAny(prompt, []string{
		"solve",
		"reason",
		"prove",
		"derive",
		"calculate",
		"step by step",
		"think carefully",
		"analyze",
		"tradeoff",
		"architecture",
		"optimization",
		"root cause",
	}) {
		return true
	}
	return hasMathExpression(prompt)
}

func simpleOrSpamPrompt(prompt string, tokens int) bool {
	normalized := normalizePrompt(prompt)
	if codingPrompt(normalized) || reasoningPrompt(normalized, tokens) {
		return false
	}
	if tokens <= 12 {
		return true
	}
	if repeatedText(normalized) {
		return true
	}
	return containsAny(normalized, []string{
		"buy now",
		"click here",
		"free money",
		"limited offer",
		"subscribe now",
		"promo code",
	})
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func hasMathExpression(value string) bool {
	hasDigit := false
	hasOperator := false
	for _, r := range value {
		if unicode.IsDigit(r) {
			hasDigit = true
		}
		switch r {
		case '+', '-', '*', '/', '=', '%':
			hasOperator = true
		}
	}
	return hasDigit && hasOperator
}

func repeatedText(value string) bool {
	words := strings.Fields(value)
	if len(words) < 6 {
		return false
	}
	counts := map[string]int{}
	for _, word := range words {
		counts[word]++
		if counts[word] >= len(words)/2 {
			return true
		}
	}
	return false
}

func normalizePrompt(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
