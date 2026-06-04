# Deployment Notes

Augur runs as one HTTP process. It does not include built-in TLS, so put it
behind your own proxy, ingress, or platform layer for public traffic.

## Build

```bash
go build -o bin/augur ./cmd/augur
go test ./...
scripts/smoke-test.sh
scripts/routing-smoke-test.sh
```

Run a real provider smoke test before launch:

```bash
export OPENAI_API_KEY="..."
export AUGUR_SMOKE_MODEL="gpt-4.1-mini"
export AUGUR_SMOKE_CHAT=1
scripts/smoke-test.sh
```

Run a bounded live learning test when you want to verify request-aware routing
against real provider calls:

```bash
cp .env.example .env.local
# edit .env.local and set OPENAI_API_KEY
scripts/live-learning-test.sh
```

The live learning script sends real requests, prints the selected backend for
each request, and verifies that learned state was saved. It is capped by request
count, not by provider billing.

Enable judge scoring for sampled quality labels:

```bash
AUGUR_LIVE_JUDGE=1 \
AUGUR_LIVE_JUDGE_MODEL=gpt-4.1-mini \
AUGUR_LIVE_JUDGE_SAMPLE_RATE=0.25 \
scripts/live-learning-test.sh
```

Judge mode sends extra provider calls for the sampled responses.

Run without hint headers to exercise automatic prompt classification:

```bash
AUGUR_LIVE_SEND_HINTS=0 scripts/live-learning-test.sh
```

## Docker

```bash
docker build -t augur:local .
cp .env.example .env.local
```

Edit `.env.local` and set `OPENAI_API_KEY`.
`AUGUR_BACKENDS` can contain one backend or many comma-separated `id=model`
pairs. Env-only mode uses round-robin by default. Use `AUGUR_CONFIG` for
cost-aware, latency-aware, or learned routing.

```bash
docker run --rm \
  -p 8080:8080 \
  --env-file .env.local \
  augur:local
```

## Config

Use `AUGUR_CONFIG` for JSON or YAML config:

```bash
export AUGUR_CONFIG="/etc/augur/config.json"
export OPENAI_API_KEY="..."
bin/augur
```

Example configs live in `configs/`. Copy one outside the repo and replace model
IDs with real provider model names.

Keep API keys in environment variables, not config files.

Use `configs/request-aware.example.yaml` when you want the bandit to learn from
request type, budget, and tier hints.

Clients can send these optional headers:

```text
X-Augur-Request-Type: reasoning
X-Augur-User-Tier: premium
X-Augur-User-ID: user-123
X-Augur-Latency-Budget-Ms: 2400
X-Augur-Cost-Budget-USD: 0.05
```

If clients do not send these headers, Augur uses a local prompt classifier. It
does not call a model before routing.

Request type is a routing signal. Route rules can match task type, tenant, and
user tier. Backend capabilities remove incompatible backends before health
filters and router selection.

Routes can also define `fallbacks`. Augur tries those backends in order when the
chosen backend fails with a retryable error before a complete response.

Routes can define `canary` for deterministic rollout. Use `shadow: true` when
you want to call the candidate backend without returning its response.

Use `data_plane.health_check.enabled: true` with the `health` filter to mark dead
backends before user traffic reaches them. Set `backends[].health_path` only when
the provider has a cheap endpoint for health checks. Set `backends[].timeout` to
cap slow attempts before fallback.

## Runtime State

If `learning.persistence.enabled` is true, Augur writes learned reward and
quality state to `learning.persistence.path`.

For local development, `.augur/learned-state.json` is fine. For deployment, use
a durable path such as:

```text
/var/lib/augur/learned-state.json
```

The state file stores learned matrices only. It does not store prompts,
responses, or API keys.

## Health

- `GET /healthz`: process is alive
- `GET /readyz`: gateway is ready
- `GET /debug/backends`: backend health, circuit, latency, and error window state

Use `/readyz` for load balancer readiness checks.
Use `/debug/backends` for operator checks. It follows gateway auth when auth is
enabled.

## Auth

Gateway auth is disabled unless `AUGUR_GATEWAY_API_KEYS` is set.

```bash
AUGUR_GATEWAY_API_KEYS=first-client-key,second-client-key
```

Clients can use either header:

```bash
Authorization: Bearer first-client-key
```

```bash
X-Augur-API-Key: first-client-key
```

Auth protects `/v1/chat/completions`. Health endpoints stay public.

## Runtime Features

- Streaming: set `"stream": true`.
- Hedging: configure `data_plane.hedge`.
- Backend capabilities: set `backends[].capabilities`.
- Route fallback chains: set `routes[].fallbacks`.
- Canary rollout: set `routes[].canary`.
- Canary rollback thresholds: configure top-level `canary`.
- Tenant limits: add `tenant` to `data_plane.filters` and configure `tenants`.
- Live learning: use `router.type: "bandit"` and `learning.enabled: true`.
- Persistence: enable `learning.persistence` before relying on learned state
  across restarts.

See [Config reference](config-reference.md) for fields.

## Operations

- Keep provider keys outside git.
- Keep gateway client keys outside git.
- Start with hedging disabled, then tune with real latency data.
- Watch latency, error rate, spend, and output quality.
- Rotate keys through your normal secret manager.

## Current Gaps

- TLS termination must be handled outside Augur.
- Kubernetes manifests are not included.
- Dashboards and alerts are not included.
