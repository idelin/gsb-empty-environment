"""Command-line replayer for the sparse semiring engine.

Usage:
    python -m sr_engine.cli init DIR M K N SEMIRING
    python -m sr_engine.cli commit DIR TXN_ID OPS_JSON
    python -m sr_engine.cli checkpoint DIR
    python -m sr_engine.cli show DIR [VERSION]
    python -m sr_engine.cli replay DIR SCRIPT_JSON

OPS_JSON is a list like [["set","A",0,1,5],["del","B",1,2]].
SCRIPT_JSON is a list of {"txn_id": ..., "ops": [...]} objects; replay
recovers the engine from DIR, applies each transaction and prints the
resulting C after every committed version.
"""

from __future__ import annotations

import json
import sys

from .engine import Engine


def _dump(eng, version=None):
    with eng.snapshot(version) as snap:
        out = {
            "version": snap.version,
            "A": [[i, j, v] for (i, j), v in sorted(snap.A.items())],
            "B": [[i, j, v] for (i, j), v in sorted(snap.B.items())],
            "C": [[i, j, v] for (i, j), v in sorted(snap.C.items())],
        }
    print(json.dumps(out))


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    if not argv:
        print(__doc__)
        return 2
    cmd, rest = argv[0], argv[1:]
    if cmd == "init":
        path, m, k, n, semiring = rest
        Engine.create(path, int(m), int(k), int(n), semiring).close()
    elif cmd == "commit":
        path, txn_id, ops_json = rest
        eng = Engine.open(path)
        print(eng.commit(txn_id, json.loads(ops_json)))
        eng.close()
    elif cmd == "checkpoint":
        eng = Engine.open(rest[0])
        eng.checkpoint()
        eng.close()
    elif cmd == "show":
        version = int(rest[1]) if len(rest) > 1 else None
        eng = Engine.open(rest[0])
        _dump(eng, version)
        eng.close()
    elif cmd == "replay":
        path, script_path = rest
        eng = Engine.open(path)
        with open(script_path) as f:
            script = json.load(f)
        for txn in script:
            eng.commit(txn["txn_id"], txn["ops"])
            _dump(eng)
        eng.close()
    else:
        print(__doc__)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
