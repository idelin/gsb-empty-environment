#!/usr/bin/env python3
"""Demo: incremental partition repair after local transition modifications.

Shows a merge repair (two blocks fuse) followed by a split repair (a block
breaks apart), each checked against a from-scratch re-minimization.
"""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from dfa_min.cli import dfa_from_json
from dfa_min.core import minimize
from dfa_min.incremental import IncrementalMinimizer


def show_mapping(title, mapping):
    print(title)
    for old, new in sorted(mapping.items()):
        print(f"  {old} -> {new}")


def main():
    data = json.loads(Path(__file__).with_name("sample_dfa.json").read_text())
    dfa = dfa_from_json(data)

    inc = IncrementalMinimizer(dfa)
    show_mapping("Initial mapping (after full minimization):", inc.mapping())

    # Edit 1: E --0--> E (was: missing, i.e. to the dead state).
    # A and E become equivalent and their blocks merge; the dead state
    # becomes unreachable and is dropped.
    report = inc.modify([("E", "0", "E")])
    print("\nEdit 1: E --0--> E")
    print(f"  removed as unreachable: {report.removed_unreachable or '(none)'}")
    print(f"  merges: {report.merges or '(none)'}")
    print(f"  splits: {report.splits or '(none)'}")
    print(f"  touched state pairs: {report.touched_pairs}")
    show_mapping("  mapping after repair:", inc.mapping())

    # Edit 2: A --1--> A (was: A --1--> B).
    # A can no longer accept anything, so {A, E} splits again.
    report = inc.modify([("A", "1", "A")])
    print("\nEdit 2: A --1--> A")
    print(f"  merges: {report.merges or '(none)'}")
    print(f"  splits: {report.splits or '(none)'}")
    print(f"  touched state pairs: {report.touched_pairs}")
    show_mapping("  mapping after repair:", inc.mapping())

    # Cross-check the repaired partition against a from-scratch minimization.
    edited = json.loads(json.dumps(data))
    edited["transitions"]["E"]["0"] = "E"
    edited["transitions"]["A"]["1"] = "A"
    fresh = minimize(dfa_from_json(edited))
    assert inc.quotient() == fresh.quotient, "incremental repair diverged!"
    print("\nCross-check: incremental result == from-scratch minimization. OK")


if __name__ == "__main__":
    main()
