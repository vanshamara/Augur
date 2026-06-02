package harness

import (
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/rng"
)

// Event is one request arriving at a moment in virtual time. Sequence breaks ties
// when two events land on the same instant, so the order is always the same.
type Event struct {
	Sequence int
	Arrival  time.Time
	Request  core.Request
}

type Trace struct {
	Seed   uint64
	Start  time.Time
	Events []Event
}

// GenerateTrace builds a reproducible list of requests. Arrivals are spaced by a
// random gap, and each request gets a type and a prompt size drawn from the seed.
// The same seed and count always give the same trace.
func GenerateTrace(seed uint64, count int, start time.Time) Trace {
	deriver := rng.NewDeriver(seed)
	events := make([]Event, count)
	at := start
	for i := 0; i < count; i++ {
		gen := deriver.Rand(uint64(i))
		gapMs := 20 + gen.IntN(80)
		at = at.Add(time.Duration(gapMs) * time.Millisecond)
		events[i] = Event{
			Sequence: i,
			Arrival:  at,
			Request: core.Request{
				ID:       "req-" + strconv.Itoa(i),
				Features: drawFeatures(gen),
			},
		}
	}
	return Trace{Seed: seed, Start: start, Events: events}
}

func drawFeatures(gen *rand.Rand) core.Features {
	types := []core.RequestType{core.Chat, core.Reasoning, core.Embedding}
	return core.Features{
		PromptTokens:    50 + gen.IntN(2000),
		Type:            types[gen.IntN(len(types))],
		LatencyBudgetMs: 1200,
		CostBudget:      0.05,
		UserTier:        "standard",
	}
}
