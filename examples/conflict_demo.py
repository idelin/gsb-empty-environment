"""Independently executable counterexample: dangling-else conflict.

Builds the classic ambiguous if/else grammar, prints the conflict report
(shortest reachable prefix + competing productions), then replays the witness
input through the parser to prove the prefix is genuinely reachable.

Run from the repository root:
    python -m examples.conflict_demo
"""
from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from grammar_compiler import Grammar, Parser
from grammar_compiler.parser import ConflictError


def build():
    g = Grammar()
    g.add_terminals("if", "then", "else", "print", "x")
    lang = g.module("lang")
    lang.add("S", ["if", "E", "then", "S", "S2"])
    lang.add("S", ["print"])
    lang.add("S2", ["else", "S"])
    lang.add("S2", [])
    lang.add("E", ["x"])
    g.set_start("lang", "S")
    return g


def replay():
    """Return (report, ConflictError) proving the witness is reachable."""
    g = build()
    snap = g.compile()
    assert snap.conflicts, "expected a conflict"
    report = snap.conflicts[0]
    parser = Parser(snap.table_dict(), snap.production_map(), snap.start)
    witness = [t.name for t in report.witness]
    try:
        parser.parse(witness)
    except ConflictError as err:
        return report, err
    raise AssertionError("witness input did not reproduce the conflict")


def main():
    report, err = replay()
    print(report)
    print()
    print("replaying witness input through the parser:")
    print(" ", err)
    consumed = " ".join(t.name for t in err.consumed)
    expected = " ".join(t.name for t in report.prefix)
    assert consumed == expected, (consumed, expected)
    print("parser consumed exactly the reported prefix before the conflict")


if __name__ == "__main__":
    main()
