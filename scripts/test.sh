#!/bin/sh
# Runs the test suite with a Node.js version that supports TypeScript
# type stripping (>= 22.6). Prefers the newest Node found via nvm/n.
set -eu

cd "$(dirname "$0")/.."

supports_ts() {
  "$1" --experimental-strip-types -e 'const x: number = 1;' >/dev/null 2>&1
}

pick_node() {
  candidates=""
  if command -v node >/dev/null 2>&1; then
    candidates="$candidates $(command -v node)"
  fi
  for dir in "$HOME"/.nvm/versions/node/* /usr/local/n/versions/node/*; do
    [ -x "$dir/bin/node" ] && candidates="$candidates $dir/bin/node"
  done
  best=""
  best_ver="0.0.0"
  for c in $candidates; do
    if supports_ts "$c"; then
      ver=$("$c" -e 'console.log(process.versions.node)')
      newest=$(printf '%s\n%s\n' "$best_ver" "$ver" | sort -t. -k1,1n -k2,2n -k3,3n | tail -1)
      if [ "$newest" = "$ver" ]; then
        best="$c"
        best_ver="$ver"
      fi
    fi
  done
  [ -n "$best" ] && echo "$best"
}

NODE_BIN=$(pick_node || true)
if [ -z "${NODE_BIN:-}" ]; then
  echo "error: need Node.js >= 22.6 (TypeScript type stripping); none found" >&2
  exit 1
fi

echo "using node: $NODE_BIN ($("$NODE_BIN" --version))"
exec "$NODE_BIN" --experimental-strip-types --disable-warning=ExperimentalWarning tests/run.ts
