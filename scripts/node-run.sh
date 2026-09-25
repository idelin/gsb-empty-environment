#!/bin/sh
# Execs the given args with a Node.js >= 22.6 (native TypeScript type-stripping
# + stable node:test). Prefers the newest nvm-installed node, falls back to PATH.
set -eu

version_ok() {
  "$1" -e 'const p=process.versions.node.split(".").map(Number);process.exit((p[0]>22||(p[0]===22&&p[1]>=6))?0:1)' 2>/dev/null
}

CANDIDATES=""
if [ -d "$HOME/.nvm/versions/node" ]; then
  CANDIDATES=$(ls -r "$HOME/.nvm/versions/node" 2>/dev/null || true)
fi
for v in $CANDIDATES; do
  bin="$HOME/.nvm/versions/node/$v/bin/node"
  if [ -x "$bin" ] && version_ok "$bin"; then
    exec "$bin" --experimental-strip-types --disable-warning=ExperimentalWarning "$@"
  fi
done
if version_ok node; then
  exec node --experimental-strip-types --disable-warning=ExperimentalWarning "$@"
fi
echo "error: Node.js >= 22.6 required (none found in nvm or PATH)" >&2
exit 1
