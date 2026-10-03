#!/bin/sh
# Pick a Node >= 23 (type stripping + node:test). Falls back to `node`.
set -e
pick() {
  for d in "$HOME"/.nvm/versions/node/v2[3-9]* "$HOME"/.nvm/versions/node/v[3-9][0-9]*; do
    [ -x "$d/bin/node" ] && echo "$d/bin/node" && return 0
  done | sort -V | tail -1
}
NODE="$(pick)"
[ -z "$NODE" ] && NODE=node
echo "using $NODE ($($NODE --version))"
case "${1:-all}" in
  demo) exec "$NODE" src/cli.ts ;;
  test) exec "$NODE" --test test/*.test.ts ;;
  all)
    "$NODE" --test test/*.test.ts
    "$NODE" src/cli.ts
    ;;
  *) echo "usage: ./run.sh [all|test|demo]" >&2; exit 2 ;;
esac
