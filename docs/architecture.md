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
4. `internal/dataplane` removes backends that do not support the request type.
5. `internal/dataplane` applies filters such as active health state, circuit
   breaking, concurrency, tenant limits, hedging, and single flight.
6. `internal/dataplane` estimates the max cost per backend and drops candidates
   over the request cost budget.
7. `internal/dataplane` applies deterministic canary assignment when a route has
   a canary rule and the canary backend is still eligible.
8. A router chooses one backend from the remaining candidates.
9. If the chosen backend fails with a retryable error before a complete
   response, `internal/dataplane` tries the route fallback chain.
10. Shadow canaries call the candidate backend without returning that response.
11. `internal/dataplane` applies any per-backend timeout, then
    `internal/backend/openai` sends the attempt to the provider.
12. Augur returns the response and sets `X-Augur-Backend`, `X-Augur-Route`,
    `X-Augur-Fallback-Count`, `X-Augur-Attempted-Backends`, cost headers, and
    canary headers when available.
13. If live learning is enabled, `internal/live` updates reward and quality
    state.

Route canary rollback uses error rate, latency regression, and health or circuit
availability.

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

- backend capability filtering
- active health filtering
- circuit breaking
- adaptive concurrency
- tenant limits
- hedging
- single flight
- backend debug status for health, circuit mode, P95 latency, and error rate

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
