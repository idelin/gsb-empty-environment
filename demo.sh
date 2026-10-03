#!/usr/bin/env bash
# Runs the demo scenario. Requires Node >= 22.6 (see test.sh).
set -euo pipefail
cd "$(dirname "$0")"

pick_node() {
  if node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null; then
    echo node; return
  fi
  for candidate in "$HOME"/.nvm/versions/node/v23*/bin/node "$HOME"/.nvm/versions/node/v22*/bin/node; do
    if [ -x "$candidate" ]; then echo "$candidate"; return; fi
  done
  echo node
}

NODE_BIN="${NODE_BIN:-$(pick_node)}"
exec "$NODE_BIN" --experimental-strip-types demo/run.ts
