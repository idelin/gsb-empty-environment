"""Subprocess worker for crash-injection recovery tests.

Usage: python tests/crash_worker.py DIR SCRIPT.json

Opens (or creates) the engine in DIR, applies the transaction script and
optionally checkpoints after given transaction indices.  Crash injection
is driven entirely by the SR_FAULT_AT / SR_FAULT_SKIP environment
variables honoured inside sr_engine.storage, so the parent can kill this
process at any persistence boundary and then rerun it to test recovery.
"""

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sr_engine import Engine  # noqa: E402


def main():
    path, script_path = sys.argv[1], sys.argv[2]
    with open(script_path) as f:
        spec = json.load(f)
    if os.path.exists(os.path.join(path, "checkpoint.json")):
        eng = Engine.open(path)
    else:
        eng = Engine.create(path, spec["m"], spec["k"], spec["n"], spec["semiring"])
    checkpoint_after = set(spec.get("checkpoint_after", []))
    for idx, txn in enumerate(spec["txns"]):
        eng.commit(txn["txn_id"], txn["ops"])
        if idx in checkpoint_after:
            eng.checkpoint()
    print(json.dumps({"version": eng.version}))
    eng.close()


if __name__ == "__main__":
    main()
