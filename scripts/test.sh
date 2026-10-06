#!/bin/sh
# Runs the replay tests with the newest available Node that supports
# native TypeScript type stripping (>= 22.6), no build step required.
set -eu

pick_node() {
  candidates=""
  if [ -d "$HOME/.nvm/versions/node" ]; then
    candidates=$(ls -v "$HOME/.nvm/versions/node" 2>/dev/null | tail -r || true)
  fi
  for version in $candidates; do
    bin="$HOME/.nvm/versions/node/$version/bin/node"
    [ -x "$bin" ] || continue
    if "$bin" -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null; then
      echo "$bin"
      return 0
    fi
  done
  if node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null; then
    command -v node
    return 0
  fi
  echo "error: need Node >= 22.6 for native TypeScript type stripping" >&2
  return 1
}

NODE_BIN=$(pick_node)
major=$("$NODE_BIN" -p 'process.versions.node.split(".")[0]')
if [ "$major" -ge 23 ]; then
  exec "$NODE_BIN" --test tests/replay.test.ts
else
  exec "$NODE_BIN" --experimental-strip-types --test tests/replay.test.ts
fi
