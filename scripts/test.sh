#!/bin/sh
# 选择支持 TypeScript 类型擦除的 Node 运行测试：
#   Node >= 23.6 原生支持；Node 22.6+ 需要 --experimental-strip-types。
set -e
cd "$(dirname "$0")/.."

node_major() { "$1" -p 'Number(process.versions.node.split(".")[0])' 2>/dev/null || echo 0; }
node_minor() { "$1" -p 'Number(process.versions.node.split(".")[1])' 2>/dev/null || echo 0; }

pick_node() {
  if [ "$(node_major node)" -ge 24 ] || { [ "$(node_major node)" -eq 23 ] && [ "$(node_minor node)" -ge 6 ]; }; then
    echo node; return
  fi
  for dir in "$HOME"/.nvm/versions/node/v*/bin; do
    [ -x "$dir/node" ] || continue
    if [ "$(node_major "$dir/node")" -ge 24 ] || { [ "$(node_major "$dir/node")" -eq 23 ] && [ "$(node_minor "$dir/node")" -ge 6 ]; }; then
      echo "$dir/node"; return
    fi
  done
  echo node
}

NODE=$(pick_node)
echo "使用 Node: $NODE ($($NODE -v))"
exec "$NODE" --test test/*.test.ts
