# Dashboards and alerts

Augur serves Prometheus metrics at `GET /metrics`. This folder has a starter
Grafana dashboard and a set of example alert rules.

## Metrics

- `augur_requests_total`: requests served, labeled by route, backend, and canary.
- `augur_errors_total`: failed requests, same labels.
- `augur_routes_total`: routing picks, labeled by router strategy and backend.
- `augur_cost_usd_total`: realized cost in USD.
- `augur_latency_ms`: request latency histogram (`_bucket`, `_sum`, `_count`).

Metrics use low-cardinality labels only. The request id is not a metric label.
It stays on trace spans, so per-request lookups go through tracing or
`/debug/decisions`, not Prometheus.

## Scrape config

```yaml
scrape_configs:
  - job_name: augur
    static_configs:
      - targets: ["augur:8080"]
```

`/metrics` is not behind gateway auth, the same as the health endpoints. Keep it
on an internal network or behind your own proxy if the gateway is public.

## Grafana

Import `augur-grafana.json` and pick your Prometheus data source when prompted.
The panels show request rate, error rate, p95 latency, spend rate, and requests
per backend.

## Alerts

`alerts.yaml` has example Prometheus rules for high error rate, high p95 latency,
no traffic, and an instance-down check. Tune the thresholds to your traffic
before relying on them.
