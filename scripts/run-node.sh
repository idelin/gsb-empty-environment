#!/usr/bin/env sh
set -eu

run_with() {
  node_bin="$1"
  shift
  if [ -x "$node_bin" ]; then
    major="$("$node_bin" -p 'Number(process.versions.node.split(".")[0])')"
    if [ "$major" -ge 22 ]; then
      exec "$node_bin" --experimental-strip-types "$@"
    fi
  fi
}

if command -v node >/dev/null 2>&1; then
  major="$(node -p 'Number(process.versions.node.split(".")[0])')"
  if [ "$major" -ge 22 ]; then
    exec node --experimental-strip-types "$@"
  fi
fi

if [ -n "${NODE_BIN:-}" ]; then
  run_with "$NODE_BIN" "$@"
fi

for candidate in "$HOME"/.nvm/versions/node/v22.*/bin/node /opt/homebrew/bin/node /usr/local/bin/node; do
  if [ -x "$candidate" ]; then
    run_with "$candidate" "$@"
  fi
done

echo "Node.js >= 22.6 is required for native TypeScript execution." >&2
exit 1
