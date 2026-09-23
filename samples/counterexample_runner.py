"""Independently executable counterexample runner.

Usage: python -m samples.counterexample_runner case.json
Exit code 0: counterexample confirmed (expectation still violated).
Exit code 1: counterexample no longer reproduces.
Exit code 2: usage / IO / decode error.
"""

from __future__ import annotations

import sys
from pathlib import Path

if __package__ in (None, ""):  # allow: python samples/counterexample_runner.py
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from treeauto import Counterexample  # noqa: E402


def main(argv=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if len(argv) != 1:
        print("usage: counterexample_runner CASE.json", file=sys.stderr)
        return 2
    try:
        payload = Path(argv[0]).read_text(encoding="utf-8")
        cx = Counterexample.from_json(payload)
    except Exception as exc:  # decode/IO problems are reported, not raised
        print(f"error: cannot load counterexample: {exc}", file=sys.stderr)
        return 2
    result = cx.replay()
    print(f"note: {cx.note or '(none)'}")
    print(f"expected_accepted={result.expected_accepted} "
          f"actual_accepted={result.actual_accepted}")
    print(f"root_states={sorted(result.run.root_states)}")
    if result.confirmed:
        print("CONFIRMED: the counterexample still reproduces")
        return 0
    print("NOT REPRODUCED: automaton now agrees with the expectation")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
