#!/usr/bin/env python3
"""Counterexample: a missing terminal must locate its expectation set.

For "id = num print id ;" the parser must point at the 'print' token
(position 3) and report exactly {';'} as the expectation set, not a vague
"parse error".  A nonterminal mismatch must likewise list the full FIRST
set of expected terminals.
"""
import _bootstrap  # noqa: F401

from parserlib import PredictiveParser
from parserlib import demolang


def main() -> None:
    parser = PredictiveParser(demolang.program_grammar())

    result = parser.parse(demolang.tokens_from_kinds("id = num print id ;"))
    assert len(result.diagnostics) == 1, result.diagnostics
    diag = result.diagnostics[0]
    assert diag.kind == "missing-terminal", diag
    assert diag.expected == (";",), diag
    assert diag.position == 3, diag

    result2 = parser.parse(demolang.tokens_from_kinds("id = ;"))
    assert len(result2.diagnostics) == 1, result2.diagnostics
    diag2 = result2.diagnostics[0]
    assert diag2.kind == "unexpected-token", diag2
    assert diag2.expected == ("(", "id", "num"), diag2
    assert diag2.found == ";", diag2
    print("PASS ce_03: missing/unexpected terminals locate exact expectation sets")


if __name__ == "__main__":
    main()
