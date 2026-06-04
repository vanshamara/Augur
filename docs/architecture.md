# Architecture

Augur is a self-hosted Go inference gateway for OpenAI-compatible chat
requests. It routes requests across configured model backends using operational
signals, request hints, policy, and optional learning.

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
2. `internal/httpapi` parses `/v1/chat/completions` and request-aware hints.
3. `internal/dataplane` matches route rules and creates the route candidate set.
4. `internal/dataplane` applies filters such as health, circuit breaking,
   concurrency, tenant limits, hedging, and single flight.
5. A router chooses one backend from the remaining candidates.
6. `internal/backend/openai` sends the request to the provider.
7. Augur returns the response and sets `X-Augur-Backend` and, when a route
   matched, `X-Augur-Route`.
8. If live learning is enabled, `internal/live` updates reward and quality state.

Route-specific fallback chains, deterministic canary percentage routing, and
backend capability filtering are planned V1 work. Current fallback behavior is
limited to load shedding retries and hedging.

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

Augur does not yet expose first-class image, audio, or video routing APIs.
