#!/usr/bin/env bash
# Picks a Node.js runtime with native TypeScript support (>= 23.6, or 22.7+ with flag)
# and runs the test suite or the demo.
set -euo pipefail
cd "$(dirname "$0")"

pick_node() {
  local candidates=(
    "node"
    "$HOME/.nvm/versions/node/v23.9.0/bin/node"
    "$HOME/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node"
  )
  for cand in "${candidates[@]}"; do
    command -v "$cand" >/dev/null 2>&1 || continue
    local ver major minor
    ver=$("$cand" -e 'process.stdout.write(process.versions.node)' 2>/dev/null) || continue
    major=${ver%%.*}
    minor=$(echo "$ver" | cut -d. -f2)
    if [ "$major" -ge 24 ] || { [ "$major" -eq 23 ] && [ "$minor" -ge 6 ]; }; then
      echo "$cand|"; return
    fi
    if { [ "$major" -eq 22 ] && [ "$minor" -ge 7 ]; } || { [ "$major" -eq 23 ]; }; then
      echo "$cand|--experimental-strip-types"; return
    fi
  done
  echo "error: need Node.js >= 22.7 (TypeScript type-stripping support)" >&2
  exit 1
}

IFS='|' read -r NODE FLAG <<< "$(pick_node)"

case "${1:-test}" in
  test) exec "$NODE" $FLAG --test tests/*.test.ts ;;
  demo) exec "$NODE" $FLAG examples/demo.ts ;;
  *) echo "usage: ./run.sh [test|demo]" >&2; exit 1 ;;
esac
