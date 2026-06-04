#!/usr/bin/env bash
set -euo pipefail

# Runs the local product-promise demo. It uses scripted in-memory backends, so
# it makes no real provider calls and costs nothing.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT"
go run ./cmd/demo
