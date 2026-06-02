# Deployment Notes

These notes cover a simple Augur process running behind your own network,
proxy, or platform layer.

Augur currently provides a local HTTP gateway. It does not yet include built-in
auth, TLS, or streaming.

## Build

Build the server binary:

```bash
go build -o bin/augur ./cmd/augur
```

Run tests before shipping a build:

```bash
go test ./...
```

## Config

Use JSON config with `AUGUR_CONFIG`:

```bash
export AUGUR_CONFIG="/etc/augur/config.json"
export OPENAI_API_KEY="..."
bin/augur
```

Public examples are in `configs/`:

- `minimal.example.json`: smallest local gateway config
- `cost-aware.example.json`: cost-aware routing with two backends
- `augur.example.json`: full local bandit config
- `deployment.example.json`: deployment-shaped bandit config

Copy one of those files outside the repo and replace model IDs with real model
names. Keep API keys in the environment, not in config files.

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
- Use `router.type: "bandit"` when live learning should guide routing.
- Enable persistence before relying on learned behavior across restarts.
- Start with hedging disabled, then tune it with real latency data.
- Watch p95 latency, p99 latency, error rate, spend, and output quality.
- Rotate provider keys through your normal secret manager.
- Keep config examples public, but keep real config private.

## Current Gaps

These are still future work:

- built-in auth
- TLS config
- streaming responses
- container and Kubernetes manifests
- multi-tenant limits
- production pricing table helpers
