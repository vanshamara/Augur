package main

import (
	"fmt"
	"time"

	"github.com/vanshamara/Augur/internal/harness"
)

// main runs every baseline router over each regime and prints one comparison table
// per regime. Run it with: go run ./cmd/compare
func main() {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const requests = 2000

	seeds := make([]uint64, 30)
	for i := range seeds {
		seeds[i] = uint64(i + 1)
	}

	for _, regime := range harness.AllRegimes() {
		comparison := harness.Compare(regime, harness.BaselineFactories(), seeds, requests, start)
		fmt.Println(comparison.String())
	}
}
