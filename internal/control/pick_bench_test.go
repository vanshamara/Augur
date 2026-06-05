package control

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/clock"
	"github.com/vanshamara/Augur/internal/core"
)

// BenchmarkBanditPick measures one full contextual-bandit routing decision:
// feature encoding, quality-gated feasibility filtering, and Thompson sampling
// over every candidate. No provider call sits on this path, so the result is the
// gateway's own routing overhead. It is parametrized by candidate count because
// the sampling step is linear in the number of arms.
func BenchmarkBanditPick(b *testing.B) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	req := request("bench")

	for _, n := range []int{3, 8, 16} {
		b.Run(fmt.Sprintf("backends=%d", n), func(b *testing.B) {
			backends := benchBackendIDs(n)
			bandit := NewBanditRouter(BanditConfig{
				Policy: NewPolicy(PolicyConfig{
					Constraints:  ConstraintConfig{MinQuality: 0.85, MaxErrorRate: 0.10},
					Objective:    ObjectiveConfig{Type: MinimizeLatency},
					OnInfeasible: InfeasibleBestEffort,
				}),
				Backends: backends,
				Clock:    clock.NewVirtual(start),
				Seed:     1,
			})
			defer bandit.Close()

			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if bandit.Pick(ctx, req, backends) == "" {
					b.Fatal("expected a backend")
				}
			}
		})
	}
}

// BenchmarkEncodeFeatures isolates the request-to-feature-vector step.
func BenchmarkEncodeFeatures(b *testing.B) {
	req := request("bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EncodeFeatures(req)
	}
}

func benchBackendIDs(n int) []core.BackendID {
	ids := make([]core.BackendID, n)
	for i := range ids {
		ids[i] = core.BackendID(fmt.Sprintf("backend-%d", i))
	}
	return ids
}
