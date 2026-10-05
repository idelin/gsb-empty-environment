#!/usr/bin/env bash
# Prints the path of a node binary >= 22.6 (needed for `node:test` and
# --experimental-strip-types). Prefers $NODE_BIN, then `node` on PATH,
# then common version-manager install locations.
set -euo pipefail

ok() {
  "$1" -e 'const v=process.versions.node.split(".").map(Number);process.exit((v[0]>22||(v[0]===22&&v[1]>=6))?0:1)' 2>/dev/null
}

candidates=()
if [[ -n "${NODE_BIN:-}" ]]; then candidates+=("$NODE_BIN"); fi
if command -v node >/dev/null 2>&1; then candidates+=("$(command -v node)"); fi
while IFS= read -r p; do candidates+=("$p"); done < <(
  ls -d "$HOME"/.nvm/versions/node/*/bin/node \
        /usr/local/n/versions/node/*/bin/node \
        /opt/homebrew/bin/node \
        /usr/local/bin/node 2>/dev/null || true
)

for c in "${candidates[@]}"; do
  if [[ -x "$c" ]] && ok "$c"; then
    echo "$c"
    exit 0
  fi
done

echo "error: need node >= 22.6 (type stripping + node:test); none found" >&2
exit 1
