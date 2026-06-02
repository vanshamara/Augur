package quality

import (
	"context"

	"github.com/vanshamara/Augur/internal/core"
)

type Result struct {
	Score  float64
	Reason string
}

type Scorer interface {
	Score(ctx context.Context, req core.Request, resp core.Response) (Result, error)
}
