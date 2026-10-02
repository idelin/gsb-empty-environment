"""Brute-force short-string edit enumeration: the reference oracle.

For short token strings this module enumerates every edit script within the
budget by plain BFS over sequences (insert / delete / substitute one token
at a time) and keeps the cheapest scripts whose result parses strictly.
The repair engine's output is cross-validated against this oracle in tests.
"""
from __future__ import annotations

from .parser import PredictiveParser
from .tokens import Token


def _tokens(kinds: tuple[str, ...]) -> list[Token]:
    return [Token(kind=k, text=k, start=p, end=p + 1) for p, k in enumerate(kinds)]


def make_strict_checker(grammar, alphabet: tuple[str, ...]):
    parser = PredictiveParser(grammar)

    def is_ok(seq: tuple[str, ...]) -> bool:
        return parser.parse(_tokens(seq)).ok

    return is_ok


def _neighbours(seq: tuple[str, ...], alphabet: tuple[str, ...]):
    out = []
    for pos in range(len(seq) + 1):
        for terminal in alphabet:
            out.append(seq[:pos] + (terminal,) + seq[pos:])
    for i in range(len(seq)):
        out.append(seq[:i] + seq[i + 1 :])
        for terminal in alphabet:
            if terminal != seq[i]:
                out.append(seq[:i] + (terminal,) + seq[i + 1 :])
    return out


def reference_repairs(
    kinds: tuple[str, ...],
    alphabet: tuple[str, ...],
    budget: int,
    is_ok,
) -> tuple[int | None, set[tuple[str, ...]]]:
    """Return (min_cost, set of repaired kind sequences) by exhaustive BFS."""
    start = tuple(kinds)
    if is_ok(start):
        return 0, {start}
    seen = {start}
    frontier = [start]
    for cost in range(1, budget + 1):
        nxt = []
        for seq in frontier:
            for neighbour in _neighbours(seq, alphabet):
                if neighbour not in seen:
                    seen.add(neighbour)
                    nxt.append(neighbour)
        good = {seq for seq in nxt if is_ok(seq)}
        if good:
            return cost, good
        frontier = nxt
    return None, set()
