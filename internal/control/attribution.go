package control

import (
	"sync"
	"time"

	"github.com/vanshamara/Augur/internal/core"
)

type DecisionRecord struct {
	RequestID          string
	Backend            core.BackendID
	Features           []float64
	PolicyID           string
	Strategy           string
	RoutingPropensity  float64
	JudgingPropensity  float64
	At                 time.Time
	ShadowBackends     []core.BackendID
	InfeasibleFallback bool
}

type ResponseRecord struct {
	RequestID string
	Response  core.Response
	At        time.Time
}

type AttributionLog struct {
	mu        sync.Mutex
	decisions map[string]DecisionRecord
	responses map[string]ResponseRecord
}

func NewAttributionLog() *AttributionLog {
	return &AttributionLog{
		decisions: map[string]DecisionRecord{},
		responses: map[string]ResponseRecord{},
	}
}

func (a *AttributionLog) RecordDecision(record DecisionRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record.Features = append([]float64(nil), record.Features...)
	record.ShadowBackends = append([]core.BackendID(nil), record.ShadowBackends...)
	a.decisions[record.RequestID] = record
}

func (a *AttributionLog) RecordResponse(record ResponseRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.responses[record.RequestID] = record
}

func (a *AttributionLog) Decision(requestID string) (DecisionRecord, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record, ok := a.decisions[requestID]
	record.Features = append([]float64(nil), record.Features...)
	record.ShadowBackends = append([]core.BackendID(nil), record.ShadowBackends...)
	return record, ok
}

func (a *AttributionLog) Response(requestID string) (ResponseRecord, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record, ok := a.responses[requestID]
	return record, ok
}
