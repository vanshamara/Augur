# Config Reference

Augur accepts JSON and YAML config files through `AUGUR_CONFIG`.

Unknown fields are rejected. Keep real config files outside the repo.

Check a config before starting the gateway:

```bash
augur validate --config configs/request-aware.example.yaml
```

## Top Level

```yaml
server: {}
openai: {}
backends: []
routes: []
pricing: {}
router: {}
data_plane: {}
learning: {}
canary: {}
tenants: {}
policy: {}
budgets: {}
rate_limit: {}
```

## Server

```yaml
server:
  addr: "127.0.0.1:8080"
  max_body_bytes: 1048576
  read_timeout: "5s"
  write_timeout: "30s"
  idle_timeout: "2m"
  shutdown_timeout: "10s"
```

- `addr`: listen address.
- `max_body_bytes`: maximum JSON request body size.
- timeout fields: Go duration strings or millisecond numbers.

## OpenAI

```yaml
openai:
  base_url: ""
  api_key_env: "OPENAI_API_KEY"
```

- `base_url`: optional OpenAI-compatible API base URL.
- `api_key_env`: environment variable that stores the provider key.

## Backends

```yaml
backends:
  - id: "fast"
    model: "your-model-id"
    provider: "openai"
    base_url: ""
    api_key_env: ""
    capabilities: ["chat", "reasoning", "coding"]
    health_path: "/healthz"
    timeout: "10s"
    input_cost_per_token: 0.0
    output_cost_per_token: 0.0
    max_completion_tokens: 512
```

- `id`: route-facing backend name. Defaults to `model` when omitted.
- `model`: provider model name. Required.
- `provider`: `openai` (default) or `anthropic`. `openai` covers OpenAI and any
  OpenAI-compatible server, such as Ollama, vLLM, or LM Studio. `anthropic` uses
  the Anthropic Messages API.
- `base_url`: optional per-backend API base URL. It overrides the top-level
  `openai.base_url`. Point it at a local server like
  `http://localhost:11434/v1` for Ollama. Anthropic backends default to the
  Anthropic API when this is empty.
- `api_key_env`: optional per-backend key environment variable. It overrides the
  top-level `openai.api_key_env`. OpenAI-compatible local servers usually need no
  key, so leave the variable unset. Anthropic backends default to
  `ANTHROPIC_API_KEY`.
- `capabilities`: optional list of supported request types. Supported values are
  `chat`, `reasoning`, `coding`, and `embedding`. If omitted or empty, the
  backend is treated as compatible with all current request types. Anthropic
  backends are the exception: omitted capabilities mean `chat`, `reasoning`, and
  `coding`, and listing `embedding` is rejected at startup.
- `health_path`: optional provider health endpoint path. It is called with `GET`
  during active checks. Leave it empty when the provider has no cheap health path.
- `timeout`: optional per-backend request timeout. It applies before fallback.
- cost fields: USD per token. These override `pricing.models`.
- `max_completion_tokens`: backend default max output token count.

## Pricing

```yaml
pricing:
  models:
    your-model-id:
      input_cost_per_token: 0.000001
      output_cost_per_token: 0.000004
```

Augur uses this table when a backend does not set explicit prices.

Prices are in dollars per single token, not per thousand or per million tokens.
Augur rejects negative prices and any price of one dollar or more per token,
since a value that large is almost always the wrong unit.

## Routes

```yaml
routes:
  - name: "reasoning"
    match:
      task_types: ["reasoning"]
      tenants: ["premium"]
      user_tiers: ["premium"]
    candidates:
      - backend: "balanced"
    fallbacks:
      - backend: "strong"
    canary:
      backend: "candidate"
      percent: 5
      sticky_key: "tenant_and_request"
      shadow: false
  - name: "default"
    candidates:
      - backend: "fast"
      - backend: "balanced"
      - backend: "strong"
```

Routes are checked in order. The first matching route supplies the candidate
backend set for the request. Empty match fields act as wildcards, so one route
with no `match` block can be used as the default route.

- `name`: route name. Must be unique.
- `match.task_types`: optional list of `chat`, `reasoning`, `coding`, or
  `embedding`.
- `match.tenants`: optional list of tenant IDs.
- `match.user_tiers`: optional list of user tiers.
- `candidates`: backend IDs that this route may use.
- `fallbacks`: ordered backend IDs to try when the chosen backend fails before
  a complete response.
- `canary.backend`: backend ID to test with percentage rollout.
- `canary.percent`: deterministic rollout percentage from `0` to `100`.
- `canary.sticky_key`: optional key. Supported values are `request_id`,
  `tenant_id`, `user_id`, `tenant_and_request`, and `tenant_and_user`.
- `canary.shadow`: when true, Augur calls the canary backend without returning
  its response.

If `routes` is empty, Augur keeps the older behavior and all configured backends
are candidates. If routes are configured and none match a request, Augur returns
no candidates. Route candidates are also filtered by backend `capabilities`
before health, circuit, concurrency, and router selection.

## Router

```yaml
router:
  type: "bandit"
  seed: 7
  alpha: 0.2
  p2c_window: 64
  weights:
    fast: 1.0
```

Supported `type` values:

- `static`
- `round_robin`
- `least_loaded`
- `ewma`
- `cost_aware`
- `p2c`
- `litellm_shuffle`
- `envoy_least_request`
- `bandit`

## Data Plane

```yaml
data_plane:
  filters: ["tenant", "health", "circuit", "concurrency"]
  health:
    fast: true
  circuit:
    failure_threshold: 3
    recovery_after: "2s"
    half_open_max: 1
  concurrency:
    initial_limit: 8
    min_limit: 1
    max_limit: 64
    target_latency_ms: 1200
  hedge:
    enabled: false
    delay: "75ms"
    max_in_flight: 4
    budget_fraction: 0.10
    trigger_percentile: 95
    max_extra_calls: 1
  single_flight:
    enabled: true
    key: "prompt"
  health_check:
    enabled: true
    interval: "5s"
    timeout: "2s"
    failure_threshold: 2
    success_threshold: 1
```

Filters run in the order listed. Supported filters are `tenant`, `health`,
`circuit`, and `concurrency`.

Hedging is disabled unless `hedge.enabled` is true.

Active health checks are disabled unless `health_check.enabled` is true. They
require the `health` filter in `filters`. A failed check marks the backend
unhealthy after `failure_threshold` consecutive failures. A recovered backend is
eligible again after `success_threshold` consecutive successes.

`GET /debug/backends` returns health, circuit state, concurrency, P95 latency,
error rate, and timeout details for each backend. It uses the same auth settings
as `/v1/chat/completions`.

## Metrics

`GET /metrics` serves Prometheus metrics with no extra exporter setup. It is
public like the health endpoints, so restrict it at the network layer if the
gateway is public. The metrics are:

- `augur_requests_total`, `augur_errors_total`: request and error counts.
- `augur_routes_total`: routing picks, labeled by router strategy and backend.
- `augur_cost_usd_total`: realized cost in USD.
- `augur_latency_ms`: latency histogram.
- `augur_quality_score`: sampled quality score histogram.
- `augur_reward`: bandit reward histogram.

Labels are low cardinality, such as backend and route. The request id is not a
metric label; it stays on trace spans. A starter Grafana dashboard and example
alert rules are in the `dashboards/` folder.

## Decision Log

```yaml
data_plane:
  decision_log:
    enabled: true
    size: 256
```

When enabled, Augur keeps the most recent routing decisions in memory. Each
record holds the route name, the candidate set, the reason each backend was
dropped, the canary assignment, and the backend Augur chose. It records prompt
token counts, fallback attempts, and a hashed canary sticky key, never prompt
text or API keys. Each record also includes `reason_summary`, a short operator
summary of the selected backend, exclusions, fallback attempts, canary rollback,
or final error.

`GET /debug/decisions` returns the recent records. `GET
/debug/decisions?request_id=ID` returns one record so you can explain why a
specific request went where it did. Both follow the same auth settings as
`/v1/chat/completions`. The default `size` is 256 when the log is enabled.

Augur also emits the same finished decision summary as an OpenTelemetry span
event named `route.decision`. The debug log controls only the in-memory lookup
endpoint. Without an OpenTelemetry pipeline, this event is a no-op.

Example:

```json
{
  "request_id": "req-123",
  "route_name": "default",
  "candidates": ["cheap", "strong"],
  "excluded": [
    {
      "backend": "strong",
      "stage": "budget",
      "reason": "estimated cost over budget"
    }
  ],
  "reason_summary": "Selected cheap; excluded strong at budget because estimated cost over budget.",
  "selected": "cheap"
}
```

## Learning

```yaml
learning:
  enabled: true
  tau: "10m"
  prior_precision: 1.0
  queue_size: 1024
  persistence:
    enabled: true
    path: ".augur/learned-state.json"
    save_every: 1
  judge:
    enabled: false
    model: "your-judge-model-id"
    seed: 11
```

Learning is optional. The gateway runs without it. Health, latency, cost, task
type, canary, and fallback all work with any router, so you can leave
`learning.enabled` off and pick a router like `round_robin` or `cost_aware`.

Live learning requires `router.type: "bandit"`. The bandit only ranks the
candidates the route rules and filters already allow. It cannot pick a backend
that capability, health, circuit, concurrency, tenant, budget, or canary rules
have removed. Learning improves the choice inside the eligible set. It never
overrides a hard constraint.

Persistence saves learned reward and quality state. It does not save prompts,
responses, or API keys.

## Canary

```yaml
canary:
  p95_regression_ratio: 0.20
  max_error_rate: 0.02
  min_samples: 20
```

These top-level values configure canary rollback thresholds for latency, error
rate, and sample count. Route-level `canary` blocks enable deterministic
percentage rollout.

Augur disables a route canary when the canary backend crosses the configured
error rate threshold, shows a P95 latency regression against the stable backend,
or becomes unavailable through health or circuit filters. A per-request cost
budget can skip a canary for that request without disabling the route canary
globally. If `canary.shadow` is true, Augur still sends stable responses to the
client and records the shadow backend outcome separately.

The HTTP response can include these headers:

- `X-Augur-Canary`: `live` or `shadow`
- `X-Augur-Canary-Backend`: canary backend ID
- `X-Augur-Canary-Rollback`: rollback reason when a canary is disabled

## Fallback

Routes can define ordered fallback chains with `fallbacks`.

Augur retries a fallback when the first backend fails with a backend timeout, 429, 5xx,
transport error, load shed, missing backend, or health-filtered primary pool. It
does not retry invalid client requests, auth failures, unsupported task types, or
client cancellation.

All attempts share the same request context. If failed attempts spend the request
cost budget, Augur stops before calling another fallback.

For streaming, Augur can fallback only before a stream is returned to the HTTP
layer. After a stream has started, errors are returned on that stream and no new
backend is called.

## Tenants

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
        temperature: 0.2
        user_tier: "premium"
```

- `header`: request header used for tenant identity.
- `default_tenant`: tenant used when the header is missing.
- `max_in_flight`: active request limit per tenant in this process.
- `max_cost_usd`: best-effort spend limit per tenant. It is not a hard ceiling. It
  is counted per process and resets on restart, so across replicas the real limit
  is this value times the replica count. It stops new requests once observed spend
  reaches the limit, but in-flight requests can still push spend past it. Treat it
  as a guardrail, not a billing cap.
- tenant `policy`: request default overrides.

Add `tenant` to `data_plane.filters` to enforce tenant limits.

## Policy

```yaml
policy:
  id: "default"
  constraints:
    max_p95_ms: 1200
    min_quality: 0.85
    max_error_rate: 0.02
    quality_gate: "mean"
  objective:
    type: "minimize_latency"
    latency_weight: 1.0
    cost_weight: 1.0
  exploration:
    cold_start_budget: 0.03
    judge_sample_rate: 0.1
    uncertainty_sampling: true
  on_infeasible: "best_effort"
```

Supported objectives are `minimize_latency`, `minimize_cost`, and `blend`.

With `blend`, latency is measured in milliseconds and cost is measured in
millionths of a dollar. For example, `latency_weight: 0.1` and `cost_weight: 1`
treat 1000 ms and $0.0001 as equal objective cost.

Use `min_quality` with judge scoring when answer quality matters. Augur treats
quality as a floor first, then chooses the lowest objective cost among feasible
backends.

Supported `on_infeasible` values are `best_effort` and `fail_closed`.

## Budgets

```yaml
budgets:
  latency_budget_ms: 1200
  cost_budget_usd: 0.01
  max_completion_tokens: 512
  temperature: 0.2
  require_pricing: true
```

These are request defaults used by the HTTP API. Tenant policy values can
override them.

When a request has a cost budget, Augur estimates the most each primary,
fallback, or canary backend could cost before routing. The estimate uses the
prompt token count, the request or backend max completion tokens, and the
backend prices. The HTTP API estimates prompt tokens locally from text length
before routing, so leave margin in tight budgets. Augur drops backends whose
estimate is over the budget. If every candidate is over budget, the request
fails with a clear over-budget error instead of calling an expensive backend.
By default, backends without a configured price are not dropped, since their
cost cannot be estimated. Set `require_pricing: true` to exclude unpriced
backends from requests that carry a cost budget.

The HTTP response can include these cost headers:

- `X-Augur-Estimated-Cost-USD`: the estimated max cost for the chosen backend.
- `X-Augur-Cost-USD`: the realized cost once the backend responds. Streaming
  responses only include the estimate, since realized cost is known after the
  stream ends.

## Rate Limit

```yaml
rate_limit:
  enabled: true
  requests_per_second: 20
  burst: 40
  tenants:
    premium:
      requests_per_second: 100
      burst: 200
```

When enabled, Augur applies a token-bucket request limit to
`/v1/chat/completions` and `/v1/embeddings`, keyed by the tenant from the
`X-Augur-Tenant` header.
`requests_per_second` and `burst` are the default for every tenant, and `tenants`
overrides specific ones. `burst` defaults to the per-second rate when unset.
Over-limit requests get HTTP 429 with `Retry-After`. The limit is per process.
The limit keys on the tenant, not the API key, since tenant names are config
values and keys are secrets kept in the environment.

## Request Hints

Clients can override request shape with headers:

```text
X-Augur-Request-Type: reasoning
X-Augur-User-Tier: premium
X-Augur-User-ID: user-123
X-Augur-Latency-Budget-Ms: 2400
X-Augur-Cost-Budget-USD: 0.05
X-Augur-Prompt-Tokens: 820
```

Supported request types are `chat`, `reasoning`, `coding`, and `embedding`.

`POST /v1/embeddings` takes an OpenAI-style embeddings request, where `input` is a
string or an array of strings. It always uses the `embedding` request type, so it
only routes to backends with the `embedding` capability. The same routing, cost
budgets, fallback, canary, and decision log apply. Embeddings cost is input
tokens only, so the request cost budget compares against the input cost.

When these values are missing, Augur infers a request type from the prompt. The
local classifier sends simple or spam-like prompts toward cheaper chat behavior
and marks coding or reasoning prompts as higher-need work. Headers and metadata
override the inferred values. `X-Augur-Prompt-Tokens` lets callers supply a
known prompt token count for routing and budget estimates.

Request type controls route matching and backend capability filtering. Augur does
not yet expose first-class non-text media request APIs.

The same values can be sent in chat request `metadata`:

```json
{
  "metadata": {
    "augur_request_type": "reasoning",
    "augur_user_tier": "premium",
    "augur_user_id": "user-123",
    "augur_latency_budget_ms": "2400",
    "augur_cost_budget_usd": "0.05",
    "augur_prompt_tokens": "820"
  }
}
```

Headers override metadata when both are present.
