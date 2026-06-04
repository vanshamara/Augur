# Deployment Notes

Augur runs as one HTTP process. It does not include built-in TLS, so put it
behind your own proxy, ingress, or platform layer for public traffic.

## Build

```bash
go build -o bin/augur ./cmd/augur
go test ./...
go run ./cmd/demo
scripts/smoke-test.sh
scripts/routing-smoke-test.sh
```

`go run ./cmd/demo` checks the six routing promises against scripted backends. It
makes no real provider calls.

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

Validate the config before starting Augur:

```bash
bin/augur validate --config /etc/augur/config.json
```

Explain a request before sending real traffic:

```bash
bin/augur explain --config /etc/augur/config.json --prompt "Say hello." --type chat
```

This uses dry-run backends. It does not need a provider API key and does not
call a provider.

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
X-Augur-Prompt-Tokens: 820
```

If clients do not send these headers, Augur uses a local prompt classifier. It
does not call a model before routing. If clients know the prompt token count,
send `X-Augur-Prompt-Tokens` so budget checks do not rely on the local text
estimate.

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
- `GET /metrics`: Prometheus metrics for request rate, errors, latency, and cost
- `GET /debug/backends`: backend health, circuit, latency, and error window state
- `GET /debug/decisions`: recent routing decisions, or one record with
  `?request_id=...`

Augur serves Prometheus metrics at `/metrics` out of the box, with no extra
exporter setup. Point a Prometheus scrape at it. A starter Grafana dashboard and
example alert rules live in the `dashboards/` folder. Metrics use low-cardinality
labels only, so the request id is not a label. `/metrics` is public like the
health endpoints, so keep it on an internal network or behind your own proxy if
the gateway is public.

Use `/readyz` for load balancer readiness checks.
Use `/debug/backends` and `/debug/decisions` for operator checks. They follow
gateway auth when auth is enabled. Do not expose debug endpoints on public
traffic without setting `AUGUR_GATEWAY_API_KEYS`.

The decision log is off by default. Turn it on with `data_plane.decision_log`.
It keeps the most recent decisions in memory only, and records token counts and
a hashed canary sticky key, never prompt text or API keys. Each record includes
`reason_summary`, which gives a short explanation of the selected backend,
excluded backends, fallback attempts, canary rollback, or final error.

Augur records the same finished decision summary on the active OpenTelemetry
span as `route.decision`. This helps trace a request across replicas when an
OpenTelemetry pipeline is installed. Without that pipeline, the event is a
no-op and `/debug/decisions` remains per process.

## Auth

Gateway auth is disabled unless `AUGUR_GATEWAY_API_KEYS` is set. Set it for any
shared, remote, or public deployment.

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

Auth protects `/v1/chat/completions`, `/debug/backends`, and
`/debug/decisions`. Health endpoints stay public.

## Rate Limiting

Turn on a per-client request rate limit with `rate_limit`:

```yaml
rate_limit:
  enabled: true
  requests_per_second: 20
  burst: 40
```

The limit applies to `/v1/chat/completions`. When `AUGUR_GATEWAY_API_KEYS` is
set, each client key gets its own token bucket. When auth is off, all traffic
shares one bucket, since there is no key to tell callers apart. `burst` defaults
to the per-second rate when you leave it unset. Over-limit requests get HTTP 429
with a `Retry-After` header. Like tenant limits, this is per process, so the
effective limit across replicas is the per-replica limit times the replica
count.

## Runtime Features

- Streaming: set `"stream": true`.
- Hedging: configure `data_plane.hedge`.
- Backend capabilities: set `backends[].capabilities`.
- Route fallback chains: set `routes[].fallbacks`.
- Canary rollout: set `routes[].canary`.
- Canary rollback thresholds: configure top-level `canary`.
- Tenant limits: add `tenant` to `data_plane.filters` and configure `tenants`.
- Per-client rate limit: enable `rate_limit`.
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
