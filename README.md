# Augur

Augur is a Go inference gateway. It accepts OpenAI-style chat requests, routes
them across configured backends, records the outcome, and can learn which
backend to prefer.

It is useful for:

- routing across multiple OpenAI-compatible models
- enforcing latency, cost, error, quality, and tenant limits
- testing routing policies with deterministic replay
- comparing learned routing against simple baseline routers

## Status

Augur is a v0 self-hosted gateway.

Built:

- OpenAI-style `/v1/chat/completions` HTTP API
- JSON and YAML config
- static, round-robin, least-loaded, EWMA, cost-aware, P2C, and bandit routers
- health, circuit, concurrency, tenant, hedging, and single-flight data-plane logic
- OpenAI-compatible backend adapter
- streaming responses
- optional gateway auth
- live learning from real gateway responses
- learned state persistence
- deterministic replay harness
- local LiteLLM-style and Envoy-style router shims
- Dockerfile and release checklist

Not included:

- managed hosting
- built-in TLS
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

## Run

Use environment config for a quick local run:

```bash
export OPENAI_API_KEY="..."
export AUGUR_BACKENDS=fast=gpt-4.1-mini,cheap=gpt-4.1-nano
go run ./cmd/augur
```

Or use a config file:

```bash
export OPENAI_API_KEY="..."
export AUGUR_CONFIG="configs/augur.example.json"
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
- `augur.example.json` and `augur.example.yaml`
- `deployment.example.json` and `deployment.example.yaml`

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
