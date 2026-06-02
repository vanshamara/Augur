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
- live learning from real gateway responses, with optional sampled judge labels and saved learned state
- distributed learning simulation with async aggregation
- OpenTelemetry spans and metric hooks
- OpenAI-compatible backend adapter
- sampled judge scorer with mocked tests
- LiteLLM-style and Envoy-style local router shims

The core router, policy, learning, replay, config loader, adapter, local HTTP
endpoint, gateway auth, learned state persistence, tenant limits, and release
docs are built. It is not yet production hardened, so defaults still need real
traffic tuning before broad deployment.

## Requirements

- Go 1.26.3 or newer

## Quick Start

Run the full test suite:

```bash
go test ./...
```

Run the local smoke test:

```bash
scripts/smoke-test.sh
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
export AUGUR_CONFIG="configs/augur.example.json"
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

- `AUGUR_CONFIG`: path to a JSON or YAML config file
- `AUGUR_ADDR`: listen address, default `127.0.0.1:8080`
- `AUGUR_OPENAI_BASE_URL`: alternate OpenAI-compatible base URL
- `AUGUR_BACKENDS`: comma-separated backends, either `id=model` or `model`

If `AUGUR_CONFIG` is not set, `AUGUR_BACKENDS` is required.

The current server supports JSON and YAML config files, non-streaming and
streaming chat completions, health and readiness endpoints, live learning, and
learned state persistence, and per-tenant limits.

Health checks:

- `GET /healthz`: process is running
- `GET /readyz`: gateway is ready to serve traffic

Optional gateway auth:

- set `AUGUR_GATEWAY_API_KEYS` to a comma-separated list of accepted client keys
- send requests with `Authorization: Bearer <key>` or `X-Augur-API-Key: <key>`
- leave it unset for local development without auth

For streaming responses, set `"stream": true` in the chat completion request.

Hedging is off in the example configs. When enabled, these settings control how
many extra backend calls Augur can make:

```yaml
data_plane:
  hedge:
    enabled: true
    delay: "75ms"
    max_in_flight: 4
    budget_fraction: 0.10
    trigger_percentile: 95
    max_extra_calls: 1
```

`budget_fraction` is the share of requests allowed to hedge.
`trigger_percentile` uses recent backend latency to decide when to launch the
backup call. `max_extra_calls` caps backup calls per request.

Tenant limits use `X-Augur-Tenant` by default. Missing headers use the
`default` tenant:

```yaml
tenants:
  header: "X-Augur-Tenant"
  default_tenant: "default"
  defaults:
    max_in_flight: 16
    max_cost_usd: 10.0
    policy:
      user_tier: "standard"
  overrides:
    premium:
      max_in_flight: 32
      max_cost_usd: 25.0
      policy:
        latency_budget_ms: 900
        cost_budget_usd: 0.02
        max_completion_tokens: 768
        user_tier: "premium"
```

Add `"tenant"` to `data_plane.filters` to enforce tenant request and cost
limits.

Public config examples:

- `configs/minimal.example.json` and `configs/minimal.example.yaml`: smallest
  local gateway config
- `configs/cost-aware.example.json` and `configs/cost-aware.example.yaml`:
  cost-aware routing with two backends
- `configs/augur.example.json` and `configs/augur.example.yaml`: full local
  bandit config
- `configs/deployment.example.json` and `configs/deployment.example.yaml`:
  deployment-shaped bandit config

Pricing can be set once per model in the `pricing.models` table:

```yaml
pricing:
  models:
    your-model-id:
      input_cost_per_token: 0.000001
      output_cost_per_token: 0.000004
```

Prices are USD per token. If a backend has no explicit price, Augur looks up the
price by `backend.model`. Backend-level `input_cost_per_token` and
`output_cost_per_token` values override the table.

With `router.type` set to `bandit`, real responses update the live reward model.
Set `learning.judge.enabled` to `true` and provide a judge model to add sampled
quality labels.

Set `learning.persistence.enabled` to `true` to save learned reward and quality
state across restarts. The example config writes to `.augur/learned-state.json`.
That file stores learned matrices only. It does not store prompts, responses, or
API keys.

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
configs/                    example public config files
docs/                       public reports
internal/backend            backend interfaces and implementations
internal/clock              real and virtual clocks
internal/control            policy, bandit, quality belief, attribution, rollback
internal/core               shared request and response types
internal/dataplane          filters, gateway helpers, circuit, limiter, single flight
internal/harness            deterministic replay and reporting
internal/httpapi            OpenAI-style HTTP API
internal/learn              single-writer learned state
internal/live               live reward and quality update loop
internal/observability      OpenTelemetry observer
internal/openaiapi          small OpenAI-compatible client
internal/persist            learned state file storage
internal/quality            mock and real judge scorers
internal/rng                deterministic random streams
internal/router             baseline and proxy-style routers
```

## Public Docs

Public docs:

- [Architecture](docs/architecture.md)
- [Config reference](docs/config-reference.md)
- [Deployment notes](docs/deployment.md)
- [Baseline report](docs/baseline-report.md)
- [Release checklist](docs/release-checklist.md)

The baseline report compares Augur against the LiteLLM-style and Envoy-style
shims.

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
- `.augur/`
- `docs-private/`

Before publishing or pushing changes, scan for keys and tokens.

## What Is Left For Production

The next phase is packaging and hardening:

- production HTTP hardening
- pricing data upkeep
- stronger production safety checks
