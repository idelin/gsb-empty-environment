#!/usr/bin/env bash
# Runs tests / demo with a Node.js that supports type stripping (>= 22.6).
set -euo pipefail
cd "$(dirname "$0")"

pick_node() {
  if node -e "const [a,b]=process.versions.node.split('.').map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)" 2>/dev/null; then
    command -v node; return
  fi
  local best=""
  for cand in "$HOME"/.nvm/versions/node/*/bin/node /usr/local/n/versions/node/*/bin/node; do
    [ -x "$cand" ] || continue
    if "$cand" -e "const [a,b]=process.versions.node.split('.').map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)" 2>/dev/null; then
      best="$cand"
    fi
  done
  if [ -z "$best" ]; then
    echo "error: Node.js >= 22.6 is required (uses --experimental-strip-types)." >&2
    exit 1
  fi
  echo "$best"
}

NODE_BIN="$(pick_node)"
case "${1:-test}" in
  test) exec "$NODE_BIN" --experimental-strip-types --test tests/*.test.ts ;;
  demo) exec "$NODE_BIN" --experimental-strip-types examples/demo.ts ;;
  *) echo "usage: ./run.sh [test|demo]" >&2; exit 1 ;;
esac
