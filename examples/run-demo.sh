#!/bin/sh
# Demo: infer schema candidates from samples, then retract the outlier.
set -e
cd "$(dirname "$0")/.."

# Need Node >= 22.6 for native TypeScript type stripping.
if node -e 'process.exit(process.features.typescript ? 0 : 1)' 2>/dev/null; then
  NODE=node
elif [ -x "$HOME/.nvm/versions/node/v23.9.0/bin/node" ]; then
  NODE="$HOME/.nvm/versions/node/v23.9.0/bin/node"
else
  echo "need Node >= 22.6 (see .nvmrc)" >&2
  exit 1
fi

echo "=== 1. infer from all 5 samples (outlier included) ==="
"$NODE" src/cli.ts examples/samples/*.json

echo
echo "=== 2. retract the outlier: range shrinks, provenance kept ==="
"$NODE" src/cli.ts --retract user-5 examples/samples/*.json
