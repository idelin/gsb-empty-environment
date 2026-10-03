#!/bin/sh
# 运行入口：自动选择支持原生 TypeScript 的 Node.js（>= 22.6）。
# 用法：./run.sh demo | ./run.sh test | ./run.sh <script.ts>
set -e

pick_node() {
  if command -v node >/dev/null 2>&1 && node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null; then
    echo "node"
    return
  fi
  for cand in \
    "$HOME/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node" \
    /usr/local/n/versions/node/*/bin/node; do
    if [ -x "$cand" ] && "$cand" -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null; then
      echo "$cand"
      return
    fi
  done
  echo "错误：需要 Node.js >= 22.6（原生 TypeScript 类型擦除支持）" >&2
  exit 1
}

NODE=$(pick_node)
case "${1:-demo}" in
  demo) exec "$NODE" src/demo.ts ;;
  test) shift; exec "$NODE" --test test/*.test.ts "$@" ;;
  *) exec "$NODE" "$@" ;;
esac
