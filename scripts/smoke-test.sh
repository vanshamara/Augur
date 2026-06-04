#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADDR="${AUGUR_SMOKE_ADDR:-127.0.0.1:18080}"
MODEL="${AUGUR_SMOKE_MODEL:-smoke-model}"
BASE_URL="${AUGUR_SMOKE_OPENAI_BASE_URL:-}"
CHAT="${AUGUR_SMOKE_CHAT:-0}"
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

if [[ "$CHAT" == "1" ]]; then
  if [[ "${OPENAI_API_KEY:-}" == "" ]]; then
    echo "AUGUR_SMOKE_CHAT=1 needs OPENAI_API_KEY to be set." >&2
    exit 1
  fi

  if [[ "$MODEL" == "smoke-model" && "$BASE_URL" == "" ]]; then
    echo "AUGUR_SMOKE_CHAT=1 needs AUGUR_SMOKE_MODEL to be set to a provider model." >&2
    exit 1
  fi

  echo "WARNING: AUGUR_SMOKE_CHAT=1 sends one real request to your provider and will incur a charge." >&2
fi

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
if ! curl -fsS "http://$ADDR/metrics" >/dev/null; then
  echo "metrics endpoint did not respond" >&2
  cat "$TMP_DIR/augur.log"
  exit 1
fi

if [[ "$CHAT" == "1" ]]; then
  CHAT_RESPONSE="$TMP_DIR/chat-response.json"
  if ! HTTP_STATUS="$(curl -sS -o "$CHAT_RESPONSE" -w "%{http_code}" "http://$ADDR/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-Augur-Tenant: smoke" \
    -d '{"model":"augur-chat","messages":[{"role":"user","content":"Say hello in one short sentence."}]}')"; then
    cat "$TMP_DIR/augur.log"
    exit 1
  fi

  if (( HTTP_STATUS < 200 || HTTP_STATUS >= 300 )); then
    echo "chat smoke request failed with HTTP $HTTP_STATUS" >&2
    cat "$CHAT_RESPONSE" >&2
    cat "$TMP_DIR/augur.log"
    exit 1
  fi
fi

echo "smoke test passed"
