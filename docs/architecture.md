# Architecture

Augur is a Go inference routing engine. It accepts chat requests, chooses a
backend, records the result, and uses those results to improve future choices.

The current server exposes a local OpenAI-style endpoint:

```text
client
  -> internal/httpapi
  -> internal/dataplane
  -> internal/router or internal/control bandit
  -> internal/backend/openai
  -> OpenAI-compatible backend
```

## Request Flow

1. `cmd/augur` loads JSON or YAML config and builds the gateway.
2. `internal/httpapi` parses `/v1/chat/completions` requests.
3. `internal/dataplane` applies filters such as health, circuit breaking, and
   concurrency limits.
4. A router picks one backend from the remaining candidates.
5. The backend adapter sends the request to an OpenAI-compatible API.
6. The response is returned to the caller.
7. If live learning is enabled, the response updates reward and quality state.

## Main Parts

`internal/core` holds the shared request, response, and outcome types.

`internal/router` contains baseline routers:

- static
- round-robin
- least-loaded
- EWMA
- cost-aware
- P2C
- LiteLLM-style weighted shuffle
- Envoy-style least-request

`internal/control` contains the learned router pieces:

- policy constraints and objectives
- feasibility gates
- bandit reward model
- quality belief model
- attribution log
- canary rollback helpers
- distributed learning simulation

`internal/dataplane` wraps routers with gateway behavior:

- health filtering
- circuit breaking
- adaptive concurrency
- hedging
- single flight

`internal/live` connects real gateway responses back into the bandit. Reward
updates come from latency, cost, and errors. Quality updates can come from a
sampled judge model.

`internal/persist` saves learned reward and quality matrices to a local JSON
file. It does not store prompts, responses, or API keys.

`internal/harness` runs deterministic replay tests against mock backends. This
is used for policy comparisons and regression tests.

## Learning State

The bandit keeps two learned models:

- reward state, used to pick a backend
- quality state, used by policy gates and optional judge labels

When persistence is enabled, Augur saves both models to the configured path.
On restart, it loads the file if the policy ID and backend set still match.

## Current Boundaries

Augur is usable as a local v0 gateway, but it is not fully production packaged
yet.

Current limits:

- JSON and YAML config only
- no deployment package or container image in this repo
- no multi-tenant quota system yet

Those limits are intentional for now. The core router, learning loop, adapter,
config loader, persistence, and tests are already in place.
