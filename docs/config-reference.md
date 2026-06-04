# Config Reference

Augur accepts JSON and YAML config files through `AUGUR_CONFIG`.

Unknown fields are rejected. Keep real config files outside the repo.

## Top Level

```yaml
server: {}
openai: {}
backends: []
pricing: {}
router: {}
data_plane: {}
learning: {}
canary: {}
tenants: {}
policy: {}
budgets: {}
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
    input_cost_per_token: 0.0
    output_cost_per_token: 0.0
    max_completion_tokens: 512
```

- `id`: route-facing backend name. Defaults to `model` when omitted.
- `model`: provider model name. Required.
- cost fields: USD per token. These override `pricing.models`.
- `max_completion_tokens`: backend fallback max output token count.

## Pricing

```yaml
pricing:
  models:
    your-model-id:
      input_cost_per_token: 0.000001
      output_cost_per_token: 0.000004
```

Augur uses this table when a backend does not set explicit prices.

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
```

Filters run in the order listed. Supported filters are `tenant`, `health`,
`circuit`, and `concurrency`.

Hedging is disabled unless `hedge.enabled` is true.

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

Live learning requires `router.type: "bandit"`.

Persistence saves learned reward and quality state. It does not save prompts,
responses, or API keys.

## Canary

```yaml
canary:
  p95_regression_ratio: 0.20
  max_error_rate: 0.02
  min_quality: 0.85
  min_samples: 20
```

These values control rollback decisions for canary checks.

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
- `max_cost_usd`: observed spend limit per tenant since process start.
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
```

These are request defaults used by the HTTP API. Tenant policy values can
override them.

## Request Hints

Clients can override request shape with headers:

```text
X-Augur-Request-Type: reasoning
X-Augur-User-Tier: premium
X-Augur-Latency-Budget-Ms: 2400
X-Augur-Cost-Budget-USD: 0.05
```

Supported request types are `chat`, `reasoning`, `coding`, and `embedding`.

When these values are missing, Augur infers a request type from the prompt. The
local classifier sends simple or spam-like prompts toward cheaper chat behavior
and marks coding or reasoning prompts as higher-need work. Headers and metadata
override the inferred values.

The same values can be sent in chat request `metadata`:

```json
{
  "metadata": {
    "augur_request_type": "reasoning",
    "augur_user_tier": "premium",
    "augur_latency_budget_ms": "2400",
    "augur_cost_budget_usd": "0.05"
  }
}
```

Headers override metadata when both are present.
