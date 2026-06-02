# Baseline Router Report

Date: June 2, 2026

This report compares Augur router policies against two local proxy-style shims:
`litellm-shuffle` and `envoy-least-request`.

## Scope

This is a routing-policy comparison. It is not a full benchmark of LiteLLM or
Envoy as products.

All rows use the same deterministic mock backends, traces, request counts, and
seeds. No real provider APIs are called.

Not measured:

- proxy overhead
- network hops, TLS, auth, or config reloads
- retries and fallback chains
- Redis or shared rate-limit tracking
- streaming behavior
- caching
- Envoy xDS, health checking, outlier detection, panic mode, or filters
- LiteLLM provider adapters, virtual keys, budgets, or spend tracking

## Shim Definitions

`litellm-shuffle` models LiteLLM-style weighted simple shuffle with equal weights
for every mock backend. Equal weights keep the comparison fair because the shim
does not get hidden latency, quality, or cost knowledge.

`envoy-least-request` models Envoy-style least request routing. With equal
weights, it samples two available backends and picks the one with fewer active
requests.

The public docs checked for this scope were:

- LiteLLM routing docs: https://docs.litellm.ai/docs/routing
- Envoy load balancing docs: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/load_balancers.html

## Setup

- Regimes: stable, rising-p99, intermittent-500s, cold-start
- Seeds: 30
- Requests per seed: 2000
- Reference: round-robin
- Command: `go run ./cmd/compare`

## Results

Lower `p95 vs round-robin` is better.

| Regime | LiteLLM-style p95 vs round-robin | Envoy-style p95 vs round-robin | Main read |
| --- | ---: | ---: | --- |
| stable | +1 ms | -43 ms | Shuffle behaves like round-robin. Least-request trims some queueing. |
| rising-p99 | +1 ms | -30 ms | Neither shim sees tail drift. EWMA reacts better here. |
| intermittent-500s | +1 ms | -33 ms | Neither shim avoids bursty errors by itself. |
| cold-start | 0 ms | -47 ms | Least-request helps load, but does not learn warmup quality or cost. |

## Takeaways

The LiteLLM-style shim is a fair random baseline. With equal weights, it mostly
matches round-robin. That is expected.

The Envoy-style shim is stronger for load smoothing because it uses active
request counts. It improves p95 by about 30 to 47 ms versus round-robin in these
runs, but it does not optimize semantic quality or cost.

Augur-specific policies can outperform the proxy-style shims when the regime has
a signal they can use, such as latency drift or cost. That result is about the
policy layer only. It does not say Augur is faster than a real LiteLLM or Envoy
deployment.
