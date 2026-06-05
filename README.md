# Augur: Request-Aware LLM Gateway

[![Test](https://github.com/vanshamara/Augur/actions/workflows/test.yml/badge.svg)](https://github.com/vanshamara/Augur/actions/workflows/test.yml)
![Go](https://img.shields.io/badge/Go-1.26.4-00ADD8?logo=go&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)
![OpenAI Compatible](https://img.shields.io/badge/OpenAI-compatible-412991?logo=openai&logoColor=white)
![Anthropic](https://img.shields.io/badge/Anthropic-supported-191919)
![Prometheus](https://img.shields.io/badge/Prometheus-metrics-E6522C?logo=prometheus&logoColor=white)

Calling an LLM is easy. Picking the right backend for each request is the hard
part. Augur sits in front of OpenAI, Anthropic, and local OpenAI-compatible
servers, then routes chat and embeddings requests by policy, health, latency,
cost, capability, and real outcomes.

Use it when one app needs cheap chat, stronger reasoning, fallback chains,
canaries, and a clear answer to why each request went where it did.

## What It Does

- Serves `/v1/chat/completions` and `/v1/embeddings`.
- Routes across OpenAI, Anthropic, and OpenAI-compatible backends.
- Filters backends by capability, health, circuit state, tenant limits, and cost
  budget before a provider call.
- Supports fallback chains, canaries, shadow canaries, hedging, and single
  flight.
- Exposes Prometheus metrics, backend debug state, and route decision records.
- Can learn from gateway outcomes when the bandit router is enabled.

## Requirements

- Go 1.26.4 or newer

## Quick Start

Run with environment config:

```bash
export OPENAI_API_KEY="..."
export AUGUR_BACKENDS=fast=gpt-4.1-nano,balanced=gpt-4.1-mini,strong=gpt-4.1
go run ./cmd/augur
```

Run with a config file:

```bash
export OPENAI_API_KEY="..."
export AUGUR_CONFIG=configs/request-aware.example.yaml
go run ./cmd/augur
```

Validate a config:

```bash
go run ./cmd/augur validate --config configs/request-aware.example.yaml
```

Preview a routing decision without calling a provider:

```bash
go run ./cmd/augur explain \
  --config configs/request-aware.example.yaml \
  --prompt "Say hello in one short sentence." \
  --type chat
```

`explain` also accepts `--request path.json`, inline JSON, or `--request -` for
stdin. `simulate` is an alias.

Send a chat request:

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

The response includes `X-Augur-Backend`. When fallback, canary, or cost data is
available, Augur adds routing headers for those too.

## Docs

- [Config reference](docs/config-reference.md)
- [Deployment notes](docs/deployment.md)
- [Architecture](docs/architecture.md)
- [Baseline router report](docs/baseline-report.md)
- [Contributing](CONTRIBUTING.md)
- [Security](SECURITY.md)
