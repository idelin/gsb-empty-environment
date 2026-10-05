#!/usr/bin/env bash
# 选择支持 TypeScript 类型擦除的 Node (>= 23.6) 执行。
set -euo pipefail
if node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>23||(a===23&&b>=6)?0:1)' 2>/dev/null; then
  exec node "$@"
fi
for d in "$HOME/.nvm/versions/node"/v2[3-9]*; do
  if [ -x "$d/bin/node" ] && "$d/bin/node" -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>23||(a===23&&b>=6)?0:1)'; then
    exec "$d/bin/node" "$@"
  fi
done
echo "error: Node >= 23.6 required (native TypeScript type stripping)" >&2
exit 1
