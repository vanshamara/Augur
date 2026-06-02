# Architecture

Augur is a self-hosted Go gateway for OpenAI-style chat requests.

```text
client
  -> internal/httpapi
  -> internal/dataplane
  -> internal/router or internal/control
  -> internal/backend/openai
  -> OpenAI-compatible provider
```

## Request Flow

1. `cmd/augur` loads config and builds the gateway.
2. `internal/httpapi` parses `/v1/chat/completions`.
3. `internal/dataplane` applies filters such as health, circuit breaking,
   concurrency, tenant limits, hedging, and single flight.
4. A router chooses one backend from the remaining candidates.
5. `internal/backend/openai` sends the request to the provider.
6. Augur returns the response and sets `X-Augur-Backend`.
7. If live learning is enabled, `internal/live` updates reward and quality state.

## Main Packages

`internal/router` contains baseline routers:

- static
- round-robin
- least-loaded
- EWMA
- cost-aware
- P2C
- LiteLLM-style weighted shuffle
- Envoy-style least-request

`internal/control` contains policy, gates, bandit learning, quality belief,
attribution, rollback helpers, and distributed learning simulation.

`internal/dataplane` applies operational controls around routing:

- health filtering
- circuit breaking
- adaptive concurrency
- tenant limits
- hedging
- single flight

`internal/harness` runs deterministic replay against mock backends. It is used
for policy comparison and regression tests.

`internal/persist` saves learned reward and quality matrices to a local JSON
file. It does not store prompts, responses, or API keys.

## Learning State

The bandit keeps two learned models:

- reward state, used to pick a backend
- quality state, used by policy gates and optional judge labels

When persistence is enabled, Augur reloads learned state only when the policy ID
and backend set match.

## Boundaries

Augur includes the local gateway, Dockerfile, config examples, and tests.

You still need to provide production hosting details such as TLS termination,
Kubernetes manifests, dashboards, and workload-specific tuning.
