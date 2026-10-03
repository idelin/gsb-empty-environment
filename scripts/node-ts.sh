#!/bin/sh
# 查找支持原生 TypeScript(类型擦除)的 Node.js >= 22.6 并执行。
# 22.x 需要显式 --experimental-strip-types;23.6+ 默认开启。
set -u

CANDIDATES="node"
for d in "$HOME"/.nvm/versions/node/*/bin/node /usr/local/n/versions/node/*/bin/node; do
  [ -x "$d" ] && CANDIDATES="$CANDIDATES $d"
done

for c in $CANDIDATES; do
  if "$c" -e 'const v=process.versions.node.split(".").map(Number);process.exit(v[0]>22||(v[0]===22&&v[1]>=6)?0:1)' 2>/dev/null; then
    case "$("$c" -p 'process.versions.node.split(".")[0]')" in
      22) exec "$c" --experimental-strip-types "$@" ;;
      *)  exec "$c" "$@" ;;
    esac
  fi
done

echo "error: 需要 Node.js >= 22.6(原生 TypeScript 类型擦除支持)" >&2
exit 1
