#!/usr/bin/env bash
# Runs the Node.js built-in test runner against the TypeScript sources.
# Requires Node >= 22.6 (type stripping). Prefers a modern Node from nvm
# when the default `node` is too old.
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
echo "Using $($NODE_BIN --version)"
exec "$NODE_BIN" --experimental-strip-types --test test/*.test.ts
