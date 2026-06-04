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
- backend capability filtering for chat, reasoning, coding, and embedding
- route-specific fallback chains for retryable upstream failures
- deterministic canary percentage rollout and shadow mode
- active health checks, circuit, concurrency, tenant, hedging, and single-flight data-plane logic
- backend debug output for health, circuit state, latency window, and error rate
- optional route-decision debug log that explains candidate filtering per request
- OpenAI-compatible backend adapter
- streaming responses
- optional gateway auth
- request hints through headers and chat request metadata
- local prompt classification for chat, reasoning, and coding requests
- live learning from real gateway responses
- learned state persistence
- deterministic replay harness
- local product-promise demo over scripted backends
- local LiteLLM-style and Envoy-style router shims
- Dockerfile and release checklist

Partial:

- operator visibility is JSON debug output only. There is no bundled dashboard.

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

The response includes `X-Augur-Backend`, which shows the final backend Augur
used. When a route fallback runs, Augur also returns `X-Augur-Fallback-Count`
and `X-Augur-Attempted-Backends`. When backend prices are configured, Augur adds
`X-Augur-Estimated-Cost-USD` and `X-Augur-Cost-USD` so you can see the estimated
and realized cost of the call.

Send request-aware hints when the caller knows the workload:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Augur-Request-Type: reasoning" \
  -H "X-Augur-User-Tier: premium" \
  -H "X-Augur-User-ID: user-123" \
  -H "X-Augur-Latency-Budget-Ms: 2400" \
  -H "X-Augur-Cost-Budget-USD: 0.05" \
  -H "X-Augur-Prompt-Tokens: 820" \
  -d '{
    "model": "augur-chat",
    "messages": [
      {"role": "user", "content": "Solve this carefully."}
    ]
  }'
```

The bandit uses these hints, token counts, and real outcomes to learn which
backend fits each request shape. If `X-Augur-Prompt-Tokens` is missing, Augur
uses a local text-length estimate before routing.

Learning is an optional advanced mode. Routing works without it. Health, latency,
cost, task type, canary, and fallback all run with any router, so you can keep
the bandit off and use a simple router. When the bandit is on, it only ranks the
backends that route rules and filters already allow. It cannot pick a backend
that a health, capability, budget, or canary rule has removed.

Routes can also define a canary backend with a deterministic percentage. Canary
responses include `X-Augur-Canary` and `X-Augur-Canary-Backend`. Shadow canaries
call the candidate backend without returning its response.

When hints are missing, Augur runs a local prompt classifier before routing. It
marks simple or spam-like prompts as cheap chat, and marks harder coding or
reasoning prompts as higher-need requests. This classifier does not call a
model, so it does not add routing cost.

When a request carries a cost budget, Augur estimates the most each primary,
fallback, or canary backend could cost before routing and drops the ones that
would go over budget. If every backend is over budget, the request fails with a
clear over-budget error instead of calling an expensive model. Set
`budgets.require_pricing: true` to also drop unpriced backends from budgeted
requests.

The request-aware example uses quality as a floor and then optimizes latency and
cost among the feasible backends. This keeps cheaper models in play without
letting cost override the configured quality target. Request type controls route
matching and backend capability filtering. It is not a complete media or task
gateway.

## Demo The Product Promises

Run the local demo to see all six routing promises in one pass:

```bash
go run ./cmd/demo
```

It checks health, latency, cost budget, task type, canary rollout, and fallback
against scripted in-memory backends. No real provider is called, so it is
deterministic and free. The command exits non-zero if any promise does not hold.

## Compare Routers

Run the deterministic comparison report:

```bash
go run ./cmd/compare
```

The report uses mock backends and does not call real provider APIs.

## Configure Routing

Routing is driven by a config file. Each backend lists its model, capabilities,
and prices. Each route says which backends can serve a kind of request, with
optional fallbacks and a canary.

```yaml
backends:
  - id: "fast"
    model: "gpt-4.1-nano"
    capabilities: ["chat"]
    max_completion_tokens: 512
  - id: "balanced"
    model: "gpt-4.1-mini"
    capabilities: ["chat", "reasoning", "coding"]
    max_completion_tokens: 768
  - id: "strong"
    model: "gpt-4.1"
    capabilities: ["reasoning", "coding"]
    max_completion_tokens: 1024
pricing:
  models:
    gpt-4.1-nano:
      input_cost_per_token: 0.0000001
      output_cost_per_token: 0.0000004
    gpt-4.1-mini:
      input_cost_per_token: 0.0000004
      output_cost_per_token: 0.0000016
    gpt-4.1:
      input_cost_per_token: 0.000002
      output_cost_per_token: 0.000008
routes:
  - name: "simple-chat"
    match:
      task_types: ["chat"]
    candidates:
      - backend: "fast"
    fallbacks:
      - backend: "balanced"
  - name: "reasoning"
    match:
      task_types: ["reasoning"]
    candidates:
      - backend: "balanced"
    fallbacks:
      - backend: "strong"
    canary:
      backend: "strong"
      percent: 5
      sticky_key: "tenant_and_request"
  - name: "default"
    candidates:
      - backend: "fast"
      - backend: "balanced"
      - backend: "strong"
router:
  type: "cost_aware"
budgets:
  cost_budget_usd: 0.02
  require_pricing: true
```

Reading the example:

- A `chat` request matches `simple-chat`, goes to `fast`, and falls back to
  `balanced` if `fast` fails with a retryable error before a response.
- A `reasoning` request goes to `balanced`, falls back to `strong`, and sends 5
  percent of traffic to `strong` as a canary. The same `tenant_and_request` key
  stays on the same side across retries.
- Anything else matches the `default` route.
- The `cost_aware` router prefers the cheapest eligible backend, and the
  `cost_budget_usd` budget drops any backend whose estimated cost is over budget.

See `configs/cost-aware.example.yaml` for a budget-focused config and
`configs/request-aware.example.yaml` for a learned-routing config.

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

## Limitations

Augur is a v0 self-hosted gateway. Know these limits before you rely on it:

- It only speaks the OpenAI-compatible chat API. There is no image, audio, video,
  or embedding-serving API surface, even though `embedding` exists as a routing
  task type.
- It only ships an OpenAI-compatible backend adapter. Any provider you use must
  expose that API shape.
- It has no built-in TLS, no Kubernetes manifests, and no bundled dashboard. Put
  it behind your own proxy and monitoring.
- The decision log lives in memory per process. In a multi-replica deployment a
  request id only resolves on the replica that served it.
- Learned routing is optional and has not been tuned against your workload. Start
  with a simple router and real traffic data.
- The baseline report compares routing policies against local shims. It does not
  claim Augur is faster than a real LiteLLM or Envoy deployment.

## Docs

- [Architecture](docs/architecture.md)
- [Config reference](docs/config-reference.md)
- [Deployment notes](docs/deployment.md)
- [Baseline report](docs/baseline-report.md)
- [Release checklist](docs/release-checklist.md)

## Security

Do not commit API keys or real deployment config.
Set `AUGUR_GATEWAY_API_KEYS` for shared, remote, or public deployments. It
protects `/v1/chat/completions`, `/debug/backends`, and `/debug/decisions`.

The repo ignores common local secret paths:

- `.env`
- `.env.*`
- `*.pem`
- `*.key`
- `secrets/`
- `.augur/`
- `docs-private/`

Before publishing, run a secret scan and review staged files.
