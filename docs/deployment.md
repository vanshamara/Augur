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

## Docker

```bash
docker build -t augur:local .
docker run --rm \
  -p 8080:8080 \
  -e OPENAI_API_KEY \
  -e AUGUR_ADDR=0.0.0.0:8080 \
  -e AUGUR_BACKENDS=primary=gpt-4.1-mini \
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

Use `/readyz` for load balancer readiness checks.

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
- Canary rollback: configure `canary`.
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
