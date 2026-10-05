#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE_BIN="$(bash "$ROOT/scripts/find-node.sh")"
cd "$ROOT"
exec "$NODE_BIN" --experimental-strip-types --disable-warning=ExperimentalWarning --test test/*.test.ts
