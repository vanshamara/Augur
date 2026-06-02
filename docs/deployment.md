# Deployment Notes

These notes cover a simple Augur process running behind your own network,
proxy, or platform layer.

Augur currently provides a local HTTP gateway. It does not yet include TLS.

## Build

Build the server binary:

```bash
go build -o bin/augur ./cmd/augur
```

Run tests before shipping a build:

```bash
go test ./...
```

Run the local smoke test:

```bash
scripts/smoke-test.sh
```

Set `AUGUR_SMOKE_CHAT=1` to include a real chat request. That mode needs a real
provider key and model:

```bash
export OPENAI_API_KEY="..."
export AUGUR_SMOKE_MODEL="your-provider-model"
export AUGUR_SMOKE_CHAT=1
scripts/smoke-test.sh
```

## Docker

Build the container image:

```bash
docker build -t augur:local .
```

Run it with environment config:

```bash
docker run --rm \
  -p 8080:8080 \
  -e OPENAI_API_KEY \
  -e AUGUR_ADDR=0.0.0.0:8080 \
  -e AUGUR_BACKENDS=primary=your-model-id \
  augur:local
```

Use your own config outside the repo for real deployments.

## Config

Use JSON or YAML config with `AUGUR_CONFIG`:

```bash
export AUGUR_CONFIG="/etc/augur/config.json"
export OPENAI_API_KEY="..."
bin/augur
```

Public examples are in `configs/`:

- `minimal.example.json` and `minimal.example.yaml`: smallest local gateway
  config
- `cost-aware.example.json` and `cost-aware.example.yaml`: cost-aware routing
  with two backends
- `augur.example.json` and `augur.example.yaml`: full local bandit config
- `deployment.example.json` and `deployment.example.yaml`: deployment-shaped
  bandit config

Copy one of those files outside the repo and replace model IDs with real model
names. Keep API keys in the environment, not in config files.

## Pricing

Prices are USD per token.

Set model prices once in `pricing.models`:

```yaml
pricing:
  models:
    your-model-id:
      input_cost_per_token: 0.000001
      output_cost_per_token: 0.000004
```

Augur matches the table key against `backend.model`. If the model is unknown,
the backend keeps a zero price. Set backend-level `input_cost_per_token` and
`output_cost_per_token` when you want a backend to override the table.

## Runtime State

If `learning.persistence.enabled` is true, Augur writes learned reward and
quality state to `learning.persistence.path`.

For local development, `.augur/learned-state.json` is fine. For a deployed
process, use a durable path such as:

```text
/var/lib/augur/learned-state.json
```

The state directory should be writable by the Augur process. The state file is
written with `0600` permissions. The file stores learned matrices only. It does
not store prompts, responses, or API keys.

## Process Example

One simple layout:

```text
/usr/local/bin/augur
/etc/augur/config.json
/etc/augur/augur.env
/var/lib/augur/
```

Example environment file:

```bash
OPENAI_API_KEY=replace-with-your-key
AUGUR_CONFIG=/etc/augur/config.json
AUGUR_GATEWAY_API_KEYS=replace-with-client-key
```

Example systemd unit:

```ini
[Unit]
Description=Augur inference gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=augur
Group=augur
EnvironmentFile=/etc/augur/augur.env
ExecStart=/usr/local/bin/augur
Restart=on-failure
RestartSec=5
StateDirectory=augur
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

## Network

Set the listen address in config:

```json
{
  "server": {
    "addr": "0.0.0.0:8080"
  }
}
```

Put Augur behind your own ingress, reverse proxy, or service mesh if you need
TLS, auth, request limits, or access logs.

## Health Checks

Augur exposes two public health endpoints:

- `GET /healthz`: process health
- `GET /readyz`: config and gateway readiness

Use `/readyz` for load balancer readiness checks. Use `/healthz` when you only
need to know if the process is alive.

## Gateway Auth

Gateway auth is disabled unless `AUGUR_GATEWAY_API_KEYS` is set. This keeps local
development simple and keeps real keys out of config files.

Set one or more accepted client keys:

```bash
AUGUR_GATEWAY_API_KEYS=first-client-key,second-client-key
```

Clients can send either header:

```bash
Authorization: Bearer first-client-key
```

```bash
X-Augur-API-Key: first-client-key
```

Auth protects `/v1/chat/completions`. Health endpoints stay public for load
balancers and process checks.

## Streaming

Augur supports OpenAI-style streaming chat completions. Set `stream` to `true`
in the request body:

```json
{
  "model": "augur-chat",
  "stream": true,
  "messages": [
    {"role": "user", "content": "Say hello in one short sentence."}
  ]
}
```

Streaming responses use Server-Sent Events and end with `data: [DONE]`.

## Hedging

Hedging can cut tail latency, but it can also raise spend because the extra
backend call still costs money. Keep it disabled until you have latency data.

```yaml
data_plane:
  hedge:
    enabled: true
    delay: "75ms"
    max_in_flight: 4
    budget_fraction: 0.10
    trigger_percentile: 95
    max_extra_calls: 1
```

`budget_fraction` limits how much traffic can hedge. `trigger_percentile` uses
recent successful latency for the chosen backend. `max_extra_calls` caps backup
calls per request. `max_in_flight` caps concurrent hedge calls for the process.

## Canary Rollback

Canary limits live in the `canary` block:

```yaml
canary:
  p95_regression_ratio: 0.20
  max_error_rate: 0.02
  min_quality: 0.85
  min_samples: 20
```

This means rollback is allowed when the canary has enough samples and any limit
is breached: p95 is more than 20 percent worse than baseline, error rate is over
2 percent, or quality is below 0.85.

## Tenants

Tenant identity comes from `X-Augur-Tenant` by default. Missing headers use the
configured default tenant.

```yaml
data_plane:
  filters: ["tenant", "health", "circuit", "concurrency"]

tenants:
  header: "X-Augur-Tenant"
  default_tenant: "default"
  defaults:
    max_in_flight: 64
    max_cost_usd: 100.0
    policy:
      user_tier: "standard"
  overrides:
    premium:
      max_in_flight: 128
      max_cost_usd: 250.0
      policy:
        latency_budget_ms: 900
        cost_budget_usd: 0.02
        max_completion_tokens: 768
        user_tier: "premium"
```

`max_in_flight` limits active requests per tenant in this process. `max_cost_usd`
limits observed spend since process start. Tenant `policy` values override
request defaults such as latency budget, cost budget, max completion tokens, and
user tier.

## Server Limits

The `server` block controls process-level HTTP safety settings:

```json
{
  "server": {
    "addr": "0.0.0.0:8080",
    "max_body_bytes": 1048576,
    "read_timeout": "5s",
    "write_timeout": "30s",
    "idle_timeout": "2m",
    "shutdown_timeout": "10s"
  }
}
```

`max_body_bytes` limits request body size before JSON decoding. The timeout
fields protect the server from slow or stuck clients and give in-flight requests
time to finish during shutdown.

## Operations Checklist

- Keep `OPENAI_API_KEY` and other provider keys outside git.
- Keep `AUGUR_GATEWAY_API_KEYS` outside git.
- Use `router.type: "bandit"` when live learning should guide routing.
- Enable persistence before relying on learned behavior across restarts.
- Start with hedging disabled, then tune it with real latency data.
- Watch p95 latency, p99 latency, error rate, spend, and output quality.
- Rotate provider keys through your normal secret manager.
- Keep config examples public, but keep real config private.

## Current Gaps

These are still future work:

- TLS config
- Kubernetes manifests
