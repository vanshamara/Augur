# Augur

Augur is a Go inference routing engine for sending LLM requests across a fleet
of backends.

It treats model serving as a routing and learning problem:

- filter unhealthy or overloaded backends
- choose a backend with baseline routers or learned policies
- measure latency, cost, errors, and quality
- replay the same traffic many times so results are repeatable
- compare Augur policies against simple proxy-style baselines

The project is built around deterministic tests and replay. The benchmark uses
mock backends, not real paid model calls.

## Status

Augur is a v0 inference routing engine.

It has the core pieces in place:

- deterministic clock and per-request random streams
- mock LLM backends with drift, errors, cost, and quality
- replay harness with latency, cost, error, quality, and regret metrics
- baseline routers: static, round-robin, least-loaded, EWMA, cost-aware, and P2C
- data-plane filters for health, circuits, concurrency, shedding, hedging, and single flight
- control-plane policy, feasibility gates, bandit learning, quality belief, canary rollback, and attribution
- distributed learning simulation with async aggregation
- OpenTelemetry spans and metric hooks
- OpenAI-compatible backend adapter
- sampled judge scorer with mocked tests
- LiteLLM-style and Envoy-style local router shims

The core router, policy, learning, replay, adapter, and local HTTP endpoint are
built. It is not yet packaged as a production gateway, so it does not include an
auth layer, full config loader, real deployment recipes, or tuned production
defaults.

## Requirements

- Go 1.26.3 or newer

## Quick Start

Run the full test suite:

```bash
go test ./...
```

Run the deterministic comparison report:

```bash
go run ./cmd/compare
```

The compare command prints one table per regime:

- stable
- rising-p99
- intermittent-500s
- cold-start

The output includes Augur routers plus local LiteLLM-style and Envoy-style
router shims. These shims are policy comparisons only. They do not measure real
LiteLLM or Envoy proxy overhead.

## Run The Local Gateway

Set one or more OpenAI-compatible backend models:

```bash
export OPENAI_API_KEY="..."
export AUGUR_BACKENDS="fast=your-model-id,stable=your-second-model-id"
go run ./cmd/augur
```

By default, the server listens on `127.0.0.1:8080`.

Send a chat completion request:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "augur-chat",
    "messages": [
      {"role": "user", "content": "Say hello in one short sentence."}
    ]
  }'
```

Optional environment variables:

- `AUGUR_ADDR`: listen address, default `127.0.0.1:8080`
- `AUGUR_OPENAI_BASE_URL`: alternate OpenAI-compatible base URL
- `AUGUR_BACKENDS`: comma-separated backends, either `id=model` or `model`

The current server supports non-streaming chat completions. Streaming, auth, and
full config files are still future work.

## OpenAI-Compatible Adapter

The real model adapter reads API keys from the environment.

```bash
export OPENAI_API_KEY="..."
```

The tests and replay harness do not need this key. They use mock servers and mock
backends. Do not put API keys in source files, test files, config examples, or
docs.

## Repository Layout

```text
cmd/augur                   local HTTP gateway
cmd/compare                 comparison runner
docs/                       public reports
internal/backend            backend interfaces and implementations
internal/clock              real and virtual clocks
internal/control            policy, bandit, quality belief, attribution, rollback
internal/core               shared request and response types
internal/dataplane          filters, gateway helpers, circuit, limiter, single flight
internal/harness            deterministic replay and reporting
internal/httpapi            OpenAI-style HTTP API
internal/learn              single-writer learned state
internal/observability      OpenTelemetry observer
internal/openaiapi          small OpenAI-compatible client
internal/quality            mock and real judge scorers
internal/rng                deterministic random streams
internal/router             baseline and proxy-style routers
```

## Baseline Report

See [docs/baseline-report.md](docs/baseline-report.md) for the current comparison
against the LiteLLM-style and Envoy-style shims.

Short version:

- LiteLLM-style weighted shuffle behaves like a fair random baseline with equal
  weights.
- Envoy-style least-request improves load smoothing.
- Augur-specific policies do better when they can use signals such as latency
  drift or cost.
- These results are about routing policy only. They are not full product
  benchmarks for LiteLLM or Envoy.

## Security

Secrets should stay out of git.

The repo ignores common local secret files:

- `.env`
- `.env.*`
- `*.pem`
- `*.key`
- `secrets/`
- `docs-private/`

Before publishing or pushing changes, scan for keys and tokens.

## What Is Left For Production

The next phase is packaging and hardening:

- production HTTP hardening
- production auth
- config files and examples
- deployment docs
- CI for tests
- tuned hedging budgets
- tuned canary thresholds
- pricing table updates
- multi-tenant limits
- stronger production safety checks
