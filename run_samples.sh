#!/bin/sh
# Run every sample end-to-end. Requires Python 3.11+.
set -e
cd "$(dirname "$0")"
PY=${PYTHON:-python3.11}

echo "== three_package_global (OR is global, greedy fails) =="
$PY main.py solve samples/three_package_global.json

echo "== with_exception (WITH is not unconditional) =="
$PY main.py solve samples/with_exception.json

echo "== diamond_shared (shared dependency counted once) =="
$PY main.py solve samples/diamond_shared.json

echo "== conflict_core (infeasible -> minimal conflict core) =="
$PY main.py solve samples/conflict_core.json

echo "== budget (exhausted -> feasible upper bound, pending) =="
$PY main.py solve samples/budget.json --budget 12

echo "== repair (incremental fix after libA 1.0.0 -> 2.0.0) =="
$PY main.py solve samples/repair_v1.json > /tmp/licsel_prev_solution.json
$PY main.py repair samples/repair_v2.json --prev /tmp/licsel_prev_solution.json

echo "== enumerate (brute-force cross-check, small graphs) =="
$PY main.py enumerate samples/three_package_global.json
