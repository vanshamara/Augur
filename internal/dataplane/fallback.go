package dataplane

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vanshamara/Augur/internal/core"
)

var (
	ErrAllBackendsFailed      = errors.New("all fallback backends failed")
	ErrFallbackBudgetExceeded = errors.New("fallback cost budget exhausted")
)

type attemptError struct {
	cause         error
	attempts      []core.BackendID
	fallbackCount int
}

func newAttemptError(cause error, attempts []core.BackendID, fallbackCount int) error {
	return &attemptError{
		cause:         cause,
		attempts:      append([]core.BackendID(nil), attempts...),
		fallbackCount: fallbackCount,
	}
}

func (e *attemptError) Error() string {
	if e.cause == nil {
		return fmt.Sprintf("%v after attempts %s", ErrAllBackendsFailed, joinBackendIDs(e.attempts))
	}
	return fmt.Sprintf("%v after attempts %s: %v", ErrAllBackendsFailed, joinBackendIDs(e.attempts), e.cause)
}

func (e *attemptError) Unwrap() error {
	return e.cause
}

func (e *attemptError) Is(target error) bool {
	return target == ErrAllBackendsFailed
}

func (e *attemptError) AttemptedBackends() []core.BackendID {
	return append([]core.BackendID(nil), e.attempts...)
}

func (e *attemptError) FallbackCount() int {
	return e.fallbackCount
}

type statusError interface {
	StatusCode() int
}

func retryableFailure(ctx context.Context, resp core.Response, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if resp.Errored {
		return true
	}
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrNoCompatibleCandidates) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrLoadShed) || errors.Is(err, ErrMissing) {
		return true
	}
	var status statusError
	if errors.As(err, &status) {
		code := status.StatusCode()
		return code == 429 || code >= 500
	}
	return true
}

func costBudgetSpent(req core.Request, spent float64) bool {
	return req.Features.CostBudget > 0 && spent >= req.Features.CostBudget
}

func fallbackCountForAttempts(attempts []core.BackendID) int {
	if len(attempts) <= 1 {
		return 0
	}
	return len(attempts) - 1
}

func annotateResponse(resp core.Response, attempts []core.BackendID, fallbackCount int, routeName string) core.Response {
	if resp.RouteName == "" {
		resp.RouteName = routeName
	}
	resp.AttemptedBackends = append([]core.BackendID(nil), attempts...)
	resp.FallbackCount = fallbackCount
	return resp
}

func annotateStreamChunk(chunk core.StreamChunk, attempts []core.BackendID, fallbackCount int) core.StreamChunk {
	chunk.AttemptedBackends = append([]core.BackendID(nil), attempts...)
	chunk.FallbackCount = fallbackCount
	return chunk
}

func joinBackendIDs(ids []core.BackendID) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, string(id))
	}
	return strings.Join(parts, ",")
}

func containsBackend(ids []core.BackendID, target core.BackendID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
