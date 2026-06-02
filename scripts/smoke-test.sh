#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADDR="${AUGUR_SMOKE_ADDR:-127.0.0.1:18080}"
MODEL="${AUGUR_SMOKE_MODEL:-smoke-model}"
BASE_URL="${AUGUR_SMOKE_OPENAI_BASE_URL:-}"
TMP_DIR="$(mktemp -d)"
PID=""

cleanup() {
  if [[ "$PID" != "" ]]; then
    kill "$PID" >/dev/null 2>&1 || true
    wait "$PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}

trap cleanup EXIT

mkdir -p "$ROOT/bin"
(
  cd "$ROOT"
  go build -o "$ROOT/bin/augur" ./cmd/augur
)

CONFIG="$TMP_DIR/augur-smoke.json"
cat > "$CONFIG" <<EOF
{
  "server": {
    "addr": "$ADDR"
  },
  "openai": {
    "base_url": "$BASE_URL",
    "api_key_env": "OPENAI_API_KEY"
  },
  "backends": [
    {
      "id": "primary",
      "model": "$MODEL",
      "max_completion_tokens": 32
    }
  ],
  "router": {
    "type": "round_robin"
  },
  "budgets": {
    "latency_budget_ms": 1200,
    "cost_budget_usd": 0.01,
    "max_completion_tokens": 32,
    "temperature": 0.2
  }
}
EOF

export OPENAI_API_KEY="${OPENAI_API_KEY:-smoke-test-key}"
export AUGUR_CONFIG="$CONFIG"

"$ROOT/bin/augur" > "$TMP_DIR/augur.log" 2>&1 &
PID="$!"

for _ in {1..50}; do
  if curl -fsS "http://$ADDR/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

if ! curl -fsS "http://$ADDR/healthz" >/dev/null; then
  cat "$TMP_DIR/augur.log"
  exit 1
fi
if ! curl -fsS "http://$ADDR/readyz" >/dev/null; then
  cat "$TMP_DIR/augur.log"
  exit 1
fi

if [[ "${AUGUR_SMOKE_CHAT:-0}" == "1" ]]; then
  curl -fsS "http://$ADDR/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-Augur-Tenant: smoke" \
    -d '{"model":"augur-chat","messages":[{"role":"user","content":"Say hello in one short sentence."}]}' \
    >/dev/null
fi

echo "smoke test passed"
