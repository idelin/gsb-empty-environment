#!/usr/bin/env python3
"""Counterexample: epsilon productions must build proper tree nodes.

An empty program is legal here: the tree must contain an explicit epsilon
leaf under statement_list (not a missing child, not a crash).  The same
holds for the nullable expr_tail of every expression.
"""
import _bootstrap  # noqa: F401

from parserlib import EPSILON, PredictiveParser
from parserlib import demolang


def main() -> None:
    parser = PredictiveParser(demolang.program_grammar())

    empty = parser.parse(demolang.tokens_from_kinds(""))
    assert empty.ok, empty.diagnostics
    eps = empty.root.find_all(EPSILON)
    assert len(eps) == 1, f"expected one epsilon leaf, got {len(eps)}"
    statement_list = empty.root.children[0]
    assert statement_list.symbol == "statement_list"
    assert statement_list.children[0] is eps[0]

    result = parser.parse(demolang.tokens_from_kinds("id = num ;"))
    assert result.ok
    tails = result.root.find_all("expr_tail")
    assert len(tails) == 1 and tails[0].children[0].symbol == EPSILON
    print("PASS ce_05: epsilon productions build explicit epsilon leaves")


if __name__ == "__main__":
    main()
