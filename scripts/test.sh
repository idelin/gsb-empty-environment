#!/bin/sh
# Run the test suite with a Node.js that supports type stripping (>= 22.6).
set -e
cd "$(dirname "$0")/.."

version_ok() {
  "$1" -e 'const [a, b] = process.versions.node.split(".").map(Number); process.exit(a > 22 || (a === 22 && b >= 6) ? 0 : 1)' 2>/dev/null
}

BIN=""
for candidate in node $(ls -d "$HOME"/.nvm/versions/node/*/bin/node 2>/dev/null | sort -Vr); do
  if version_ok "$candidate"; then
    BIN="$candidate"
    break
  fi
done

if [ -z "$BIN" ]; then
  echo "error: need Node.js >= 22.6 (type stripping) to run the tests" >&2
  exit 1
fi

exec "$BIN" --no-warnings --test test/*.test.ts
