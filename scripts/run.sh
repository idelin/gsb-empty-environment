#!/bin/sh
# Pick a Node.js >= 22.6 (native TypeScript type stripping). Falls back to
# the newest nvm-installed Node when the default `node` is too old.
node_ok() {
  "$1" -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=6)?0:1)' 2>/dev/null
}
if node_ok node; then
  exec node --experimental-strip-types "$@"
fi
for dir in $(ls -d "$HOME"/.nvm/versions/node/v* 2>/dev/null | sort -rV); do
  if node_ok "$dir/bin/node"; then
    exec "$dir/bin/node" --experimental-strip-types "$@"
  fi
done
echo "error: Node.js >= 22.6 is required (native TypeScript type stripping)." >&2
exit 1
