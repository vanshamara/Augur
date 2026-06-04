#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADDR="${AUGUR_LIVE_ADDR:-127.0.0.1:18081}"
ENV_FILE="${AUGUR_LIVE_ENV_FILE:-.env.local}"
ROUNDS="${AUGUR_LIVE_ROUNDS:-8}"
MAX_TOKENS="${AUGUR_LIVE_MAX_TOKENS:-80}"
MAX_REQUESTS="${AUGUR_LIVE_MAX_REQUESTS:-60}"
SLEEP_SECONDS="${AUGUR_LIVE_SLEEP_SECONDS:-0.2}"
FAST_MODEL="${AUGUR_LIVE_FAST_MODEL:-gpt-4.1-nano}"
BALANCED_MODEL="${AUGUR_LIVE_BALANCED_MODEL:-gpt-4.1-mini}"
STRONG_MODEL="${AUGUR_LIVE_STRONG_MODEL:-gpt-4.1}"
FAST_INPUT_COST="${AUGUR_LIVE_FAST_INPUT_COST:-0.0000001}"
FAST_OUTPUT_COST="${AUGUR_LIVE_FAST_OUTPUT_COST:-0.0000004}"
BALANCED_INPUT_COST="${AUGUR_LIVE_BALANCED_INPUT_COST:-0.0000004}"
BALANCED_OUTPUT_COST="${AUGUR_LIVE_BALANCED_OUTPUT_COST:-0.0000016}"
STRONG_INPUT_COST="${AUGUR_LIVE_STRONG_INPUT_COST:-0.000002}"
STRONG_OUTPUT_COST="${AUGUR_LIVE_STRONG_OUTPUT_COST:-0.000008}"
JUDGE_ENABLED="${AUGUR_LIVE_JUDGE:-0}"
JUDGE_MODEL="${AUGUR_LIVE_JUDGE_MODEL:-gpt-4.1-mini}"
JUDGE_SAMPLE_RATE="${AUGUR_LIVE_JUDGE_SAMPLE_RATE:-0.25}"
SEND_HINTS="${AUGUR_LIVE_SEND_HINTS:-1}"
TMP_DIR="$(mktemp -d)"
PID=""

load_env_file() {
  local file="$1"
  local line=""
  local key=""
  local value=""

  if [[ ! -f "$file" ]]; then
    return
  fi

  while IFS= read -r line; do
    case "$line" in
      "" | \#*)
        continue
        ;;
    esac

    if [[ "$line" != *=* ]]; then
      continue
    fi

    key="${line%%=*}"
    value="${line#*=}"
    value="${value%$'\r'}"
    value="${value%\"}"
    value="${value#\"}"
    value="${value%\'}"
    value="${value#\'}"

    case "$key" in
      OPENAI_API_KEY | AUGUR_GATEWAY_API_KEYS)
        if [[ -z "${!key:-}" ]]; then
          export "$key=$value"
        fi
        ;;
    esac
  done < "$file"
}

cleanup() {
  if [[ "$PID" != "" ]]; then
    kill "$PID" >/dev/null 2>&1 || true
    wait "$PID" >/dev/null 2>&1 || true
  fi

  if [[ "${AUGUR_LIVE_KEEP_ARTIFACTS:-0}" == "1" ]]; then
    echo "kept artifacts in $TMP_DIR"
    return
  fi

  rm -rf "$TMP_DIR"
}

write_config() {
  local config_path="$1"
  local state_path="$2"

  cat > "$config_path" <<EOF
server:
  addr: "$ADDR"
  max_body_bytes: 1048576
  read_timeout: "5s"
  write_timeout: "2m"
  idle_timeout: "2m"
  shutdown_timeout: "10s"
openai:
  api_key_env: "OPENAI_API_KEY"
backends:
  - id: "fast"
    model: "$FAST_MODEL"
    max_completion_tokens: $MAX_TOKENS
  - id: "balanced"
    model: "$BALANCED_MODEL"
    max_completion_tokens: $MAX_TOKENS
  - id: "strong"
    model: "$STRONG_MODEL"
    max_completion_tokens: $MAX_TOKENS
pricing:
  models:
    $FAST_MODEL:
      input_cost_per_token: $FAST_INPUT_COST
      output_cost_per_token: $FAST_OUTPUT_COST
    $BALANCED_MODEL:
      input_cost_per_token: $BALANCED_INPUT_COST
      output_cost_per_token: $BALANCED_OUTPUT_COST
    $STRONG_MODEL:
      input_cost_per_token: $STRONG_INPUT_COST
      output_cost_per_token: $STRONG_OUTPUT_COST
router:
  type: "bandit"
  seed: 19
data_plane:
  filters: ["tenant", "health", "circuit", "concurrency"]
  circuit:
    failure_threshold: 3
    recovery_after: "2s"
    half_open_max: 1
  concurrency:
    initial_limit: 8
    min_limit: 1
    max_limit: 64
    target_latency_ms: 1200
learning:
  enabled: true
  tau: "10m"
  prior_precision: 1.0
  queue_size: 1024
  persistence:
    enabled: true
    path: "$state_path"
    save_every: 1
  judge:
    enabled: $JUDGE_ENABLED
    model: "$JUDGE_MODEL"
    seed: 11
policy:
  id: "live-learning-test-v1"
  constraints:
    max_p95_ms: 2400
    min_quality: 0.85
    max_error_rate: 0.05
    quality_gate: "mean"
  objective:
    type: "blend"
    latency_weight: 0.1
    cost_weight: 1.0
  exploration:
    cold_start_budget: 0.05
    judge_sample_rate: $JUDGE_SAMPLE_RATE
    uncertainty_sampling: true
  on_infeasible: "best_effort"
budgets:
  latency_budget_ms: 1200
  cost_budget_usd: 0.02
  max_completion_tokens: $MAX_TOKENS
  temperature: 0.2
EOF
}

send_request() {
  local request_type="$1"
  local tier="$2"
  local latency_budget="$3"
  local cost_budget="$4"
  local prompt="$5"
  local body_path="$TMP_DIR/request-body.json"
  local response_path="$TMP_DIR/response-body.json"
  local header_path="$TMP_DIR/response-headers.txt"
  local status=""
  local backend=""
  local backend_line=""
  local curl_args=()

  cat > "$body_path" <<EOF
{
  "model": "augur-chat",
  "messages": [
    {
      "role": "user",
      "content": "$prompt"
    }
  ]
}
EOF

  : > "$response_path"
  : > "$header_path"

  curl_args=(
    -sS
    -o "$response_path"
    -D "$header_path"
    -w "%{http_code}"
    "http://$ADDR/v1/chat/completions"
    -H "Content-Type: application/json"
    -H "X-Augur-Tenant: live-test"
    -d "@$body_path"
  )

  if [[ "$SEND_HINTS" == "true" ]]; then
    curl_args+=(
      -H "X-Augur-Request-Type: $request_type"
      -H "X-Augur-User-Tier: $tier"
      -H "X-Augur-Latency-Budget-Ms: $latency_budget"
      -H "X-Augur-Cost-Budget-USD: $cost_budget"
    )
  fi

  if ! status="$(curl "${curl_args[@]}")"; then
    echo "request failed before a complete HTTP response" >&2
    cat "$response_path" >&2
    cat "$TMP_DIR/augur.log" >&2
    exit 1
  fi

  if (( status < 200 || status >= 300 )); then
    echo "request failed with HTTP $status" >&2
    cat "$response_path" >&2
    cat "$TMP_DIR/augur.log" >&2
    exit 1
  fi

  backend_line="$(grep -i '^X-Augur-Backend:' "$header_path" | tail -n 1 || true)"
  backend="$(printf '%s' "${backend_line#*:}" | tr -d '\r' | xargs)"

  if [[ "$backend" == "" ]]; then
    echo "missing X-Augur-Backend header" >&2
    cat "$header_path" >&2
    exit 1
  fi

  echo "$request_type $backend"
}

record_choice() {
  local round="$1"
  local result="$2"
  local request_type=""
  local backend=""

  read -r request_type backend <<< "$result"

  if [[ "$request_type" == "" || "$backend" == "" ]]; then
    echo "live request did not return a backend choice" >&2
    exit 1
  fi

  case "$request_type:$backend" in
    chat:fast)
      count_chat_fast=$((count_chat_fast + 1))
      ;;
    chat:balanced)
      count_chat_balanced=$((count_chat_balanced + 1))
      ;;
    chat:strong)
      count_chat_strong=$((count_chat_strong + 1))
      ;;
    reasoning:fast)
      count_reasoning_fast=$((count_reasoning_fast + 1))
      ;;
    reasoning:balanced)
      count_reasoning_balanced=$((count_reasoning_balanced + 1))
      ;;
    reasoning:strong)
      count_reasoning_strong=$((count_reasoning_strong + 1))
      ;;
    coding:fast)
      count_coding_fast=$((count_coding_fast + 1))
      ;;
    coding:balanced)
      count_coding_balanced=$((count_coding_balanced + 1))
      ;;
    coding:strong)
      count_coding_strong=$((count_coding_strong + 1))
      ;;
    *)
      echo "unexpected backend choice $request_type:$backend" >&2
      exit 1
      ;;
  esac

  printf 'round %02d %-9s -> %s\n' "$round" "$request_type" "$backend"
}

sum_state_updates() {
  local section="$1"
  local path="$2"

  awk -v section="$section" '
    $0 ~ "^  \"" section "\":" {
      in_section = 1
      next
    }
    in_section && $0 ~ /^  "[a-z_]+":/ {
      exit
    }
    in_section && $1 == "\"Updates\":" {
      value = $2
      gsub(",", "", value)
      sum += value
    }
    END {
      printf "%.3f", sum
    }
  ' "$path"
}

trap cleanup EXIT

load_env_file "$ENV_FILE"

if [[ "${OPENAI_API_KEY:-}" == "" ]]; then
  echo "OPENAI_API_KEY is required. Put it in .env.local or export it first." >&2
  exit 1
fi

if ! [[ "$ROUNDS" =~ ^[0-9]+$ ]]; then
  echo "AUGUR_LIVE_ROUNDS must be a positive integer." >&2
  exit 1
fi

if ! [[ "$MAX_TOKENS" =~ ^[0-9]+$ ]]; then
  echo "AUGUR_LIVE_MAX_TOKENS must be a positive integer." >&2
  exit 1
fi

case "$JUDGE_ENABLED" in
  1 | true)
    JUDGE_ENABLED="true"
    ;;
  0 | false)
    JUDGE_ENABLED="false"
    ;;
  *)
    echo "AUGUR_LIVE_JUDGE must be 0, 1, true, or false." >&2
    exit 1
    ;;
esac

case "$SEND_HINTS" in
  1 | true)
    SEND_HINTS="true"
    ;;
  0 | false)
    SEND_HINTS="false"
    ;;
  *)
    echo "AUGUR_LIVE_SEND_HINTS must be 0, 1, true, or false." >&2
    exit 1
    ;;
esac

if [[ "$JUDGE_SAMPLE_RATE" == "" ]]; then
  echo "AUGUR_LIVE_JUDGE_SAMPLE_RATE must be set." >&2
  exit 1
fi

TOTAL_REQUESTS=$((ROUNDS * 4))

if (( TOTAL_REQUESTS <= 0 )); then
  echo "AUGUR_LIVE_ROUNDS must be greater than zero." >&2
  exit 1
fi

if (( TOTAL_REQUESTS > MAX_REQUESTS )); then
  echo "refusing to send $TOTAL_REQUESTS requests because AUGUR_LIVE_MAX_REQUESTS is $MAX_REQUESTS" >&2
  exit 1
fi

mkdir -p "$ROOT/bin"
(
  cd "$ROOT"
  go build -o "$ROOT/bin/augur" ./cmd/augur
)

CONFIG="$TMP_DIR/live-learning.yaml"
STATE="$TMP_DIR/live-learning-state.json"
write_config "$CONFIG" "$STATE"

export AUGUR_CONFIG="$CONFIG"

"$ROOT/bin/augur" > "$TMP_DIR/augur.log" 2>&1 &
PID="$!"

for _ in {1..80}; do
  if curl -fsS "http://$ADDR/healthz" >/dev/null 2>&1; then
    break
  fi

  sleep 0.1
done

if ! curl -fsS "http://$ADDR/readyz" >/dev/null; then
  cat "$TMP_DIR/augur.log" >&2
  exit 1
fi

echo "running $TOTAL_REQUESTS live requests against $FAST_MODEL, $BALANCED_MODEL, $STRONG_MODEL"
echo "max completion tokens per request: $MAX_TOKENS"
echo "judge enabled: $JUDGE_ENABLED"
echo "judge model: $JUDGE_MODEL"
echo "judge sample rate: $JUDGE_SAMPLE_RATE"
echo "send hint headers: $SEND_HINTS"

count_chat_fast=0
count_chat_balanced=0
count_chat_strong=0
count_reasoning_fast=0
count_reasoning_balanced=0
count_reasoning_strong=0
count_coding_fast=0
count_coding_balanced=0
count_coding_strong=0

CHAT_PROMPT="Answer in one short sentence: what is Augur?"
REASONING_PROMPT="Solve carefully but keep the answer short: if 8 workers finish 24 tasks in 6 hours, how many tasks can 12 workers finish in 9 hours?"
CODING_PROMPT="Write a tiny Go function named Add that returns the sum of two ints."
COST_PROMPT="Summarize this in one short sentence: routing should balance speed, cost, and quality."

for ((round = 1; round <= ROUNDS; round++)); do
  chat_result="$(send_request \
    "chat" \
    "free" \
    "900" \
    "0.002" \
    "$CHAT_PROMPT")"
  record_choice "$round" "$chat_result"

  reasoning_result="$(send_request \
    "reasoning" \
    "premium" \
    "2600" \
    "0.05" \
    "$REASONING_PROMPT")"
  record_choice "$round" "$reasoning_result"

  coding_result="$(send_request \
    "coding" \
    "standard" \
    "2200" \
    "0.02" \
    "$CODING_PROMPT")"
  record_choice "$round" "$coding_result"

  cost_result="$(send_request \
    "chat" \
    "free" \
    "1600" \
    "0.001" \
    "$COST_PROMPT")"
  record_choice "$round" "$cost_result"

  sleep "$SLEEP_SECONDS"
done

echo
echo "backend counts by request type"
printf '%-9s %-8s %d\n' "chat" "fast" "$count_chat_fast"
printf '%-9s %-8s %d\n' "chat" "balanced" "$count_chat_balanced"
printf '%-9s %-8s %d\n' "chat" "strong" "$count_chat_strong"
printf '%-9s %-8s %d\n' "reasoning" "fast" "$count_reasoning_fast"
printf '%-9s %-8s %d\n' "reasoning" "balanced" "$count_reasoning_balanced"
printf '%-9s %-8s %d\n' "reasoning" "strong" "$count_reasoning_strong"
printf '%-9s %-8s %d\n' "coding" "fast" "$count_coding_fast"
printf '%-9s %-8s %d\n' "coding" "balanced" "$count_coding_balanced"
printf '%-9s %-8s %d\n' "coding" "strong" "$count_coding_strong"

if [[ ! -s "$STATE" ]]; then
  echo "learned state was not saved" >&2
  cat "$TMP_DIR/augur.log" >&2
  exit 1
fi

STATE_BYTES="$(wc -c < "$STATE" | xargs)"
REWARD_UPDATES="$(sum_state_updates "reward" "$STATE")"
QUALITY_UPDATES="$(sum_state_updates "quality" "$STATE")"

if [[ "$JUDGE_ENABLED" == "true" ]]; then
  if ! awk -v value="$QUALITY_UPDATES" 'BEGIN { exit !(value > 0) }'; then
    echo "judge was enabled but quality updates were not saved" >&2
    cat "$TMP_DIR/augur.log" >&2
    exit 1
  fi
fi

echo
echo "learned state saved: $STATE_BYTES bytes"
echo "reward updates saved: $REWARD_UPDATES"
echo "quality updates saved: $QUALITY_UPDATES"
echo "live learning test passed"
