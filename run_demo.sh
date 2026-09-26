#!/bin/sh
# End-to-end demo: safe / unsafe / budget-unknown / tampering rejection.
set -u
cd "$(dirname "$0")"
export PYTHONPATH=src
PY=$(command -v python3.11 || command -v python3)
DEMO_DIR="examples/certs/_demo"
mkdir -p "$DEMO_DIR"

echo "### 1. Safe model -> reachability closure"
"$PY" -m modelchecker.cli check two_bit_counter -o "$DEMO_DIR/safe.json"
"$PY" -m modelchecker.cli verify two_bit_counter "$DEMO_DIR/safe.json"

echo "\n### 2. Unsafe model -> replayable counterexample (exit 2)"
"$PY" -m modelchecker.cli check trap_unsafe -o "$DEMO_DIR/cex.json"
"$PY" -m modelchecker.cli verify trap_unsafe "$DEMO_DIR/cex.json"

echo "\n### 3. Budget exhausted -> UNKNOWN, no certificate (exit 3)"
"$PY" -m modelchecker.cli check long_chain --budget 3

echo "\n### 4. Delete a required successor -> verifier rejects (exit 1)"
"$PY" - "$DEMO_DIR/safe.json" "$DEMO_DIR/tampered.json" <<'PYEOF'
import json, sys
src, dst = sys.argv[1], sys.argv[2]
cert = json.load(open(src))
cert["successors"]["1"].remove(2)
json.dump(cert, open(dst, "w"), indent=2)
PYEOF
"$PY" -m modelchecker.cli verify two_bit_counter "$DEMO_DIR/tampered.json"

# The final verify intentionally returns 1 (invalid certificate); the demo
# itself completes successfully.
exit 0
