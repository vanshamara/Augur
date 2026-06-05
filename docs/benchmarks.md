# Benchmarks

Wall-clock microbenchmarks and a reliability ablation for the gateway hot path.

These complement the [baseline router report](baseline-report.md). That report
compares routing *policies* under a virtual clock; this one measures the
*gateway's own overhead* in real wall-clock time.

## Conditions

- Hardware: Apple M2 Pro (darwin/arm64)
- Go: 1.26.4
- Single goroutine unless noted
- Backends are in-process mocks. No network, no provider APIs.
- Reproduce: `go test -bench=. -benchmem ./internal/control/ ./internal/dataplane/`

These numbers are machine-specific and measured against mock backends. They isolate
the gateway's CPU cost, not real provider latency or production throughput.

## Routing decision overhead

The cost of choosing a backend, with no provider call on the path.

| Path | 3 backends | 8 | 16 |
| --- | ---: | ---: | ---: |
| End-to-end decision (route, capability, health/circuit/concurrency/tenant, budget, canary, pick) | 0.79 us | 1.17 us | 2.56 us |
| Contextual-bandit pick (encode, quality-gated filter, Thompson sampling per arm) | 2.36 us | 4.62 us | 8.79 us |

`EncodeFeatures` alone is 33 ns. Against a 200 ms or longer LLM call, the full
decision is under 0.01% of request time.

Source: `internal/control/pick_bench_test.go`,
`internal/dataplane/decision_bench_test.go`.

## Gateway call: added latency and throughput

Full `gateway.Call` against a zero-latency backend: filter chain, acquire and
release, the backend call, router observation, and status update.

| Benchmark | ns/op | Throughput | Notes |
| --- | ---: | ---: | --- |
| `BenchmarkGatewayCall` | 3,490 | ~285K req/s | single goroutine, 61 allocs/op |
| `BenchmarkGatewayCallParallel` | 3,901 | ~256K req/s | aggregate across cores |

The latency the gateway adds over a direct provider call is about 3.5 us. The
parallel run does not beat the single-goroutine run: the gateway is contention-bound
on shared status and stat-tracker locks, so throughput does not scale across cores
yet (sharding or lock-free counters are the next step). Real throughput is
provider-bound regardless; this number only shows the gateway is not the bottleneck.

Source: `internal/dataplane/throughput_bench_test.go`.

## Reliability: circuit breaker outage containment

A controlled ablation: two backends, one of which errors on every call, round-robin
routing, 2,000 requests.

| Configuration | Observed error rate |
| --- | ---: |
| No circuit breaker | 50.0% |
| Circuit breaker (FailureThreshold 5) | 0.25% |

Without the breaker, round-robin keeps sending half the traffic to the dead backend.
With it, the breaker trips after 5 failures and routes away; the residual 0.25% is
the failures before it trips. This simulates a backend outage in a controlled
setup, not production traffic.

Source: `internal/dataplane/circuit_ablation_test.go`.

## Correctness regression tests

- `internal/control/quality_ranking_test.go` checks that the online quality model
  recovers the correct quality ordering across backend and request-type cells on
  synthetic labels (88.9% pairwise). It is a learning-loop sanity check, not a
  production calibration claim.
