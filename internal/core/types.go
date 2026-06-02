package core

type BackendID string

type RequestType string

const (
	Chat      RequestType = "chat"
	Reasoning RequestType = "reasoning"
	Embedding RequestType = "embedding"
)

// Features describe a request in the terms routing cares about.
type Features struct {
	PromptTokens    int
	Type            RequestType
	LatencyBudgetMs int
	CostBudget      float64
	UserTier        string
}

type Request struct {
	ID       string
	Features Features
}

// Outcome is what a backend produced for one request: how long it took, what it
// cost, how many tokens it returned, and whether it failed.
type Outcome struct {
	LatencyMs    float64
	CostUSD      float64
	OutputTokens int
	Errored      bool
}

// Response is an Outcome tagged with the backend that produced it.
type Response struct {
	RequestID string
	Backend   BackendID
	Outcome
}
