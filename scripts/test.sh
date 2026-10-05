#!/usr/bin/env bash
# Runs the test suite with a Node.js version that supports type stripping
# (>= 22.6). Prefers the `node` on PATH, then scans nvm installations.
set -euo pipefail
cd "$(dirname "$0")/.."

version_ok() {
  # $1: path to node binary; prints nothing, returns 0 if usable.
  local v
  v="$("$1" -p 'process.versions.node' 2>/dev/null)" || return 1
  local major minor
  major="${v%%.*}"
  minor="$(printf '%s' "${v#*.}" | cut -d. -f1)"
  if [ "$major" -gt 22 ]; then return 0; fi
  if [ "$major" -eq 22 ] && [ "$minor" -ge 6 ]; then return 0; fi
  return 1
}

needs_flag() {
  # Type stripping is unflagged since 23.6; 22.6+ needs the flag.
  local v major minor
  v="$("$1" -p 'process.versions.node')"
  major="${v%%.*}"
  minor="$(printf '%s' "${v#*.}" | cut -d. -f1)"
  if [ "$major" -gt 23 ]; then return 1; fi
  if [ "$major" -eq 23 ] && [ "$minor" -ge 6 ]; then return 1; fi
  return 0
}

NODE=""
if version_ok node; then
  NODE="node"
else
  for candidate in $(ls -d "$HOME"/.nvm/versions/node/*/bin/node 2>/dev/null | sort -rV); do
    if version_ok "$candidate"; then
      NODE="$candidate"
      break
    fi
  done
fi

if [ -z "$NODE" ]; then
  echo "error: need Node.js >= 22.6 (type stripping) to run the tests" >&2
  exit 1
fi

if needs_flag "$NODE"; then
  exec "$NODE" --experimental-strip-types --test test/*.test.ts
else
  exec "$NODE" --test test/*.test.ts
fi
