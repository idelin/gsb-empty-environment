#!/usr/bin/env python3
"""Counterexample: recovery must terminate and never pop forever.

Adversarial inputs (unbalanced parentheses, keyword runs, empty input) make
naive "pop on error" loops spin or re-report the same position.  Every
iteration of this parser either consumes a token or pops a stack symbol, and
a hard step bound backs that up; diagnostics are de-duplicated by position.
"""
import _bootstrap  # noqa: F401

from parserlib import PredictiveParser
from parserlib import demolang

ADVERSARIAL = [
    "( ( ( ( ( (",
    ") ) ) )",
    "id id id id",
    "= = ; ;",
    "print print print",
    "( ) ; ( ; )",
    "+ + + +",
    "",
]


def main() -> None:
    parser = PredictiveParser(demolang.program_grammar())
    for kinds in ADVERSARIAL:
        result = parser.parse(demolang.tokens_from_kinds(kinds))
        positions = [d.position for d in result.diagnostics]
        assert len(positions) == len(set(positions)), (
            f"duplicate report at one position for {kinds!r}: {positions}"
        )
    print(f"PASS ce_02: {len(ADVERSARIAL)} adversarial inputs terminate, no duplicate positions")


if __name__ == "__main__":
    main()
