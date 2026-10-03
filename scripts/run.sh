#!/bin/sh
# 选择支持 TypeScript type-stripping 与 node:test 的 Node (>=22.6) 执行传入参数。
set -e

node_ok() {
  "$1" -e "const [a,b]=process.versions.node.split('.').map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)" 2>/dev/null
}

NODE=""
if node_ok node; then
  NODE="node"
else
  for d in "$HOME/.nvm/versions/node"/*; do
    [ -x "$d/bin/node" ] || continue
    if node_ok "$d/bin/node"; then NODE="$d/bin/node"; fi
  done
fi

if [ -z "$NODE" ]; then
  echo "error: need Node.js >= 22.6 (native TypeScript + node:test)" >&2
  exit 1
fi

exec "$NODE" "$@"
