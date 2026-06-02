#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

(
  cd "$ROOT"
  go test ./cmd/augur -run TestHTTPGatewayRoutesCostAwareToCheapestOpenAIBackend -count=1
)

echo "routing smoke test passed"
