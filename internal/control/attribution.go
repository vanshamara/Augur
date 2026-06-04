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

const defaultAttributionLogSize = 4096

type AttributionLog struct {
	mu        sync.Mutex
	size      int
	ids       []string
	slots     map[string]int
	next      int
	decisions map[string]DecisionRecord
	responses map[string]ResponseRecord
}

func NewAttributionLog() *AttributionLog {
	return NewAttributionLogWithSize(defaultAttributionLogSize)
}

func NewAttributionLogWithSize(size int) *AttributionLog {
	if size <= 0 {
		size = defaultAttributionLogSize
	}
	return &AttributionLog{
		size:      size,
		ids:       make([]string, size),
		slots:     map[string]int{},
		decisions: map[string]DecisionRecord{},
		responses: map[string]ResponseRecord{},
	}
}

func (a *AttributionLog) RecordDecision(record DecisionRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.remember(record.RequestID) {
		return
	}
	record.Features = append([]float64(nil), record.Features...)
	record.ShadowBackends = append([]core.BackendID(nil), record.ShadowBackends...)
	a.decisions[record.RequestID] = record
}

func (a *AttributionLog) RecordResponse(record ResponseRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.remember(record.RequestID) {
		return
	}
	record.Response = cloneResponse(record.Response)
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
	record.Response = cloneResponse(record.Response)
	return record, ok
}

func (a *AttributionLog) remember(requestID string) bool {
	if requestID == "" {
		return false
	}
	if slot, ok := a.slots[requestID]; ok {
		a.ids[slot] = ""
		delete(a.slots, requestID)
	}

	if old := a.ids[a.next]; old != "" {
		delete(a.decisions, old)
		delete(a.responses, old)
		delete(a.slots, old)
	}
	a.ids[a.next] = requestID
	a.slots[requestID] = a.next
	a.next = (a.next + 1) % a.size
	return true
}

func cloneResponse(resp core.Response) core.Response {
	resp.AttemptedBackends = append([]core.BackendID(nil), resp.AttemptedBackends...)
	return resp
}
