#!/usr/bin/env python3
"""Counterexample: naive recovery swallows the legal tail of the input.

A "skip everything up to EOF" (or skip-to-statement-end) recovery would
discard statements 2..3 after the bad '+' in statement 2.  FOLLOW-set
synchronisation must keep them: all three statements stay in the tree and
exactly one diagnostic is reported.
"""
import _bootstrap  # noqa: F401

from parserlib import PredictiveParser
from parserlib import demolang


def main() -> None:
    parser = PredictiveParser(demolang.program_grammar())
    result = parser.parse(
        demolang.tokens_from_kinds("id = num ; print + id ; id = num ;")
    )
    statements = result.root.find_all("statement")
    assert len(statements) == 3, f"legal tail swallowed: {len(statements)} statements"
    assert len(result.diagnostics) == 1, result.diagnostics
    diag = result.diagnostics[0]
    assert diag.expected == ("(", "id", "num"), diag
    # The third statement subtree is completely error-free.
    third = statements[2]
    assert not any(n.is_error for n in third._walk()), "error leaked into statement 3"
    print("PASS ce_01: FOLLOW sync keeps the legal tail (3 statements, 1 diagnostic)")


if __name__ == "__main__":
    main()
