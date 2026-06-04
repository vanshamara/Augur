package dataplane

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/vanshamara/Augur/internal/core"
)

// RouteDecisionRecord explains one routing decision after the fact. It holds the
// route candidate set, why each backend was dropped, the canary assignment, and
// the backend Augur finally used. It never stores prompt text or API keys.
type RouteDecisionRecord struct {
	RequestID         string            `json:"request_id"`
	TenantID          string            `json:"tenant_id,omitempty"`
	RouteName         string            `json:"route_name,omitempty"`
	RequestType       core.RequestType  `json:"request_type,omitempty"`
	PromptTokens      int               `json:"prompt_tokens"`
	LatencyBudgetMs   int               `json:"latency_budget_ms,omitempty"`
	CostBudgetUSD     float64           `json:"cost_budget_usd,omitempty"`
	Candidates        []core.BackendID  `json:"candidates"`
	Excluded          []ExclusionRecord `json:"excluded,omitempty"`
	ReasonSummary     string            `json:"reason_summary,omitempty"`
	Canary            CanaryRecord      `json:"canary"`
	Selected          core.BackendID    `json:"selected,omitempty"`
	AttemptedBackends []core.BackendID  `json:"attempted_backends,omitempty"`
	FallbackCount     int               `json:"fallback_count,omitempty"`
	EstimatedCostUSD  float64           `json:"estimated_cost_usd,omitempty"`
	Error             string            `json:"error,omitempty"`
}

// ExclusionRecord is one backend that a stage removed from the candidate set,
// with a short reason an operator can read.
type ExclusionRecord struct {
	Backend core.BackendID `json:"backend"`
	Stage   string         `json:"stage"`
	Reason  string         `json:"reason"`
}

// CanaryRecord describes how the canary rule resolved for one request. The
// sticky key is stored as a hash so the raw tenant or user key never lands in
// the decision log.
type CanaryRecord struct {
	Configured     bool           `json:"configured"`
	Assigned       bool           `json:"assigned"`
	Mode           string         `json:"mode,omitempty"`
	Backend        core.BackendID `json:"backend,omitempty"`
	StickyKeyHash  string         `json:"sticky_key_hash,omitempty"`
	RollbackReason string         `json:"rollback_reason,omitempty"`
}

func (r *RouteDecisionRecord) addExclusions(stage string, reason string, backends []core.BackendID) {
	if r == nil {
		return
	}
	for _, id := range backends {
		r.Excluded = append(r.Excluded, ExclusionRecord{Backend: id, Stage: stage, Reason: reason})
	}
}

func (r *RouteDecisionRecord) finish(resp core.Response, err error) {
	if r == nil {
		return
	}
	if resp.Backend != "" {
		r.Selected = resp.Backend
	}
	if len(resp.AttemptedBackends) > 0 {
		r.AttemptedBackends = append([]core.BackendID(nil), resp.AttemptedBackends...)
	}
	if resp.FallbackCount > 0 {
		r.FallbackCount = resp.FallbackCount
	}
	if resp.EstimatedCostUSD > 0 {
		r.EstimatedCostUSD = resp.EstimatedCostUSD
	}
	if err != nil {
		r.Error = err.Error()
		var metadata attemptMetadata
		if len(r.AttemptedBackends) == 0 && errors.As(err, &metadata) {
			r.AttemptedBackends = metadata.AttemptedBackends()
			r.FallbackCount = metadata.FallbackCount()
		}
	}
	r.ReasonSummary = r.reasonSummary()
}

// DecisionLog keeps the most recent decision records in a fixed size ring so an
// operator can look one up by request id without unbounded memory growth.
type DecisionLog struct {
	mu   sync.Mutex
	size int
	ids  []string
	byID map[string]*RouteDecisionRecord
	next int
}

func NewDecisionLog(size int) *DecisionLog {
	if size <= 0 {
		return nil
	}
	return &DecisionLog{
		size: size,
		ids:  make([]string, size),
		byID: make(map[string]*RouteDecisionRecord, size),
	}
}

func (l *DecisionLog) put(record *RouteDecisionRecord) {
	if l == nil || record == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	saved := record.clone()
	if existing, ok := l.byID[saved.RequestID]; ok {
		*existing = saved
		return
	}
	if old := l.ids[l.next]; old != "" {
		delete(l.byID, old)
	}
	l.ids[l.next] = saved.RequestID
	l.byID[saved.RequestID] = &saved
	l.next = (l.next + 1) % l.size
}

func (l *DecisionLog) Lookup(requestID string) (RouteDecisionRecord, bool) {
	if l == nil {
		return RouteDecisionRecord{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	record, ok := l.byID[requestID]
	if !ok {
		return RouteDecisionRecord{}, false
	}
	return record.clone(), true
}

// Recent returns the stored records from oldest to newest.
func (l *DecisionLog) Recent() []RouteDecisionRecord {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]RouteDecisionRecord, 0, len(l.byID))
	for i := 0; i < l.size; i++ {
		index := (l.next + i) % l.size
		id := l.ids[index]
		if id == "" {
			continue
		}
		if record, ok := l.byID[id]; ok {
			out = append(out, record.clone())
		}
	}
	return out
}

type attemptMetadata interface {
	AttemptedBackends() []core.BackendID
	FallbackCount() int
}

type streamCostMetadata interface {
	BackendID() core.BackendID
	EstimatedCostUSD() float64
}

func (r RouteDecisionRecord) clone() RouteDecisionRecord {
	r.Candidates = append([]core.BackendID(nil), r.Candidates...)
	r.Excluded = append([]ExclusionRecord(nil), r.Excluded...)
	r.AttemptedBackends = append([]core.BackendID(nil), r.AttemptedBackends...)
	return r
}

func (r RouteDecisionRecord) reasonSummary() string {
	parts := []string{}
	if r.Selected != "" {
		parts = append(parts, selectedSummary(r.Selected, r.AttemptedBackends, r.FallbackCount))
	} else if r.Error != "" {
		parts = append(parts, "No backend selected")
	}
	if len(r.Excluded) > 0 {
		parts = append(parts, "excluded "+exclusionSummary(r.Excluded))
	}
	if r.Canary.RollbackReason != "" {
		parts = append(parts, fmt.Sprintf("canary %s skipped because %s", r.Canary.Backend, humanizeReason(r.Canary.RollbackReason)))
	}
	if r.Error != "" {
		parts = append(parts, "error: "+r.Error)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + "."
}

func selectedSummary(selected core.BackendID, attempts []core.BackendID, fallbackCount int) string {
	if fallbackCount <= 0 || len(attempts) == 0 {
		return fmt.Sprintf("Selected %s", selected)
	}
	return fmt.Sprintf("Selected %s after attempts %s", selected, humanJoinBackends(attempts))
}

func exclusionSummary(exclusions []ExclusionRecord) string {
	limit := len(exclusions)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, 0, limit+1)
	for _, exclusion := range exclusions[:limit] {
		parts = append(parts, fmt.Sprintf("%s at %s because %s", exclusion.Backend, exclusion.Stage, exclusion.Reason))
	}
	if remaining := len(exclusions) - limit; remaining > 0 {
		parts = append(parts, fmt.Sprintf("%d more", remaining))
	}
	return humanJoin(parts)
}

func humanJoinBackends(values []core.BackendID) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, string(value))
	}
	return humanJoin(parts)
}

func humanJoin(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " and " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", and " + values[len(values)-1]
	}
}

func humanizeReason(value string) string {
	return strings.ReplaceAll(value, "_", " ")
}

// dropped returns the backends that were in before but not in after. Filters
// only remove candidates, so this is the set a stage excluded.
func dropped(before []core.BackendID, after []core.BackendID) []core.BackendID {
	if len(before) == len(after) {
		return nil
	}
	kept := make(map[core.BackendID]bool, len(after))
	for _, id := range after {
		kept[id] = true
	}
	out := make([]core.BackendID, 0, len(before)-len(after))
	for _, id := range before {
		if !kept[id] {
			out = append(out, id)
		}
	}
	return out
}

func stickyKeyHash(req core.Request, rule CanaryRule) string {
	if rule.Backend == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(stickyValue(req, rule.StickyKey)))
	return hex.EncodeToString(sum[:])
}
