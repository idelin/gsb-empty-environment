#!/bin/sh
# Runs the test suite with a Node.js that supports TypeScript type
# stripping (>= 22.6). Falls back to scanning ~/.nvm/versions/node.
set -e
cd "$(dirname "$0")"

supports_ts() {
  "$1" -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null
}

NODE_BIN=""
if supports_ts "$(command -v node)"; then
  NODE_BIN="$(command -v node)"
else
  for d in "$HOME"/.nvm/versions/node/v*; do
    if [ -x "$d/bin/node" ] && supports_ts "$d/bin/node"; then
      NODE_BIN="$d/bin/node"
      break
    fi
  done
fi

if [ -z "$NODE_BIN" ]; then
  echo "error: need Node.js >= 22.6 (TypeScript type stripping)" >&2
  exit 1
fi

echo "using $NODE_BIN ($("$NODE_BIN" --version))"
if "$NODE_BIN" -e 'process.exit(process.versions.node.split(".")[0]==="22"?0:1)'; then
  exec "$NODE_BIN" --experimental-strip-types --test test/channel.test.ts test/pipeline.test.ts
else
  exec "$NODE_BIN" --test test/channel.test.ts test/pipeline.test.ts
fi
