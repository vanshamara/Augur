# Augur

Augur is a self-hosted inference gateway. It accepts OpenAI-compatible chat
requests and routes them across configured model backends using health, latency,
cost, request shape, and optional learning.

It is useful for:

- routing across multiple OpenAI-compatible models
- enforcing latency, cost, error, quality, and tenant limits
- sending simple and complex requests to different backend pools
- testing routing policies with deterministic replay
- comparing learned routing against simple baseline routers

## Status

Augur is a v0 self-hosted gateway.

Built or mostly built:

- OpenAI-style `/v1/chat/completions` HTTP API
- JSON and YAML config
- static, round-robin, least-loaded, EWMA, cost-aware, P2C, and bandit routers
- route rules with task, tenant, tier, and candidate backend matching
- health, circuit, concurrency, tenant, hedging, and single-flight data-plane logic
- OpenAI-compatible backend adapter
- streaming responses
- optional gateway auth
- request hints through headers and chat request metadata
- local prompt classification for chat, reasoning, and coding requests
- live learning from real gateway responses
- learned state persistence
- deterministic replay harness
- local LiteLLM-style and Envoy-style router shims
- Dockerfile and release checklist

Partial:

- task type affects routing features, but backend capability filtering is not a
  first-class config field yet
- canary rollback helpers exist, but deterministic percentage rollout is not a
  first-class route rule yet
- fallback exists for load shedding and hedging, but route-specific fallback
  chains for normal upstream errors are not built yet
- health filtering and circuit breaking exist, but active health checks are not
  built yet

Not included:

- managed hosting
- built-in TLS
- deterministic canary percentage routing
- route-specific fallback chains
- active health checking
- Kubernetes manifests
- production dashboards
- real traffic tuning for your workload

## Requirements

- Go 1.26.3 or newer

## Test

Run the full suite:

```bash
go test ./...
```

Run the local startup smoke test:

```bash
scripts/smoke-test.sh
```

Run the multi-backend routing smoke test:

```bash
scripts/routing-smoke-test.sh
```

Run a real provider smoke test:

```bash
export OPENAI_API_KEY="..."
export AUGUR_SMOKE_MODEL="gpt-4.1-mini"
export AUGUR_SMOKE_CHAT=1
scripts/smoke-test.sh
```

Run a bounded live learning test:

```bash
cp .env.example .env.local
# edit .env.local and set OPENAI_API_KEY
scripts/live-learning-test.sh
```

The live learning test sends real requests through Augur and verifies that
learned state was saved. It limits request count, not provider billing.

Enable judge scoring for sampled quality labels:

```bash
AUGUR_LIVE_JUDGE=1 \
AUGUR_LIVE_JUDGE_MODEL=gpt-4.1-mini \
AUGUR_LIVE_JUDGE_SAMPLE_RATE=0.25 \
scripts/live-learning-test.sh
```

Judge mode sends extra provider calls for the sampled responses.

Run the same live test without hint headers to exercise automatic prompt
classification:

```bash
AUGUR_LIVE_SEND_HINTS=0 scripts/live-learning-test.sh
```

## Run

Use environment config for a quick local run:

```bash
export OPENAI_API_KEY="..."
export AUGUR_BACKENDS=fast=gpt-4.1-nano,balanced=gpt-4.1-mini,strong=gpt-4.1
go run ./cmd/augur
```

Or use a config file:

```bash
export OPENAI_API_KEY="..."
export AUGUR_CONFIG="configs/request-aware.example.yaml"
go run ./cmd/augur
```

Send a request:

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

The response includes `X-Augur-Backend`, which shows the backend Augur picked.

Send request-aware hints when the caller knows the workload:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Augur-Request-Type: reasoning" \
  -H "X-Augur-User-Tier: premium" \
  -H "X-Augur-Latency-Budget-Ms: 2400" \
  -H "X-Augur-Cost-Budget-USD: 0.05" \
  -d '{
    "model": "augur-chat",
    "messages": [
      {"role": "user", "content": "Solve this carefully."}
    ]
  }'
```

The bandit uses these hints, token estimates, and real outcomes to learn which
backend fits each request shape.

When hints are missing, Augur runs a local prompt classifier before routing. It
marks simple or spam-like prompts as cheap chat, and marks harder coding or
reasoning prompts as higher-need requests. This classifier does not call a
model, so it does not add routing cost.

The request-aware example uses quality as a floor and then optimizes latency and
cost among the feasible backends. This keeps cheaper models in play without
letting cost override the configured quality target. Request type is currently a
routing feature, not a complete media or task gateway.

## Compare Routers

Run the deterministic comparison report:

```bash
go run ./cmd/compare
```

The report uses mock backends and does not call real provider APIs.

## Config

Public examples are in `configs/`:

- `minimal.example.json` and `minimal.example.yaml`
- `cost-aware.example.json` and `cost-aware.example.yaml`
- `request-aware.example.json` and `request-aware.example.yaml`
- `augur.example.json` and `augur.example.yaml`
- `deployment.example.json` and `deployment.example.yaml`

For local Docker runs, copy the env example and fill in your real key:

```bash
cp .env.example .env.local
```

`AUGUR_BACKENDS` is a comma-separated list of `id=model` pairs. Env-only mode is
for quick local runs and uses round-robin routing by default. Use a config file
when you want cost-aware, latency-aware, or learned backend selection.

Keep real config files and API keys outside the repo.

## Docs

- [Architecture](docs/architecture.md)
- [Config reference](docs/config-reference.md)
- [Deployment notes](docs/deployment.md)
- [Baseline report](docs/baseline-report.md)
- [Release checklist](docs/release-checklist.md)

## Security

Do not commit API keys or real deployment config.

The repo ignores common local secret paths:

- `.env`
- `.env.*`
- `*.pem`
- `*.key`
- `secrets/`
- `.augur/`
- `docs-private/`

Before publishing, run a secret scan and review staged files.
