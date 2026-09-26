"""Standalone counterexample verifier (independent NFA interpreter).

Usage:  python -m automata.verify case.json

case.json = {"nfa_a": <nfa>, "nfa_b": <nfa>, "word": [symbols...]}
Exit code 0 iff the word is a valid counterexample to L(A) (subseteq) L(B),
i.e. A accepts it and B rejects it.  Uses only NFA.simulate -- no
partitioning, determinization or caching -- so the verdict is independent
of the search machinery that produced the witness.
"""
from __future__ import annotations

import json
import sys

from .intervals import decode
from .nfa import NFA


def verify_case(path: str) -> bool:
    with open(path, "r", encoding="utf-8") as fh:
        case = json.load(fh)
    a = NFA.from_dict(case["nfa_a"])
    b = NFA.from_dict(case["nfa_b"])
    word = tuple(case["word"])
    accepted_a = a.simulate(word)
    accepted_b = b.simulate(word)
    print(f"word        : {decode(word)!r} {word!r}")
    print(f"A ({a.name}) accepts : {accepted_a}")
    print(f"B ({b.name}) accepts : {accepted_b}")
    valid = accepted_a and not accepted_b
    print(f"valid counterexample to A (subseteq) B : {valid}")
    return valid


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(__doc__)
        return 2
    return 0 if verify_case(argv[1]) else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
