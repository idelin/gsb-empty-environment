"""Brute-force edit enumeration used as an independent reference oracle.

For short token strings this enumerates every edited string within a cost
bound and checks acceptance with the plain parser, providing a reference to
verify the minimal repair costs computed by repair.py.
"""
from __future__ import annotations

from .tokens import EOF_SYMBOL, eof_token, source_token


def _neighbors(types, alphabet):
    n = len(types)
    for i in range(n):
        yield types[:i] + types[i + 1:]  # delete
        for a in alphabet:
            if a != types[i]:
                yield types[:i] + (a,) + types[i + 1:]  # substitute
    for i in range(n + 1):
        for a in alphabet:
            yield types[:i] + (a,) + types[i:]  # insert


def accepts(types, parser):
    tokens = [source_token(t, t, k) for k, t in enumerate(types)]
    tokens.append(eof_token(len(types)))
    return parser.parse(tokens).ok


def min_accept_cost(tokens, parser, alphabet, max_cost):
    """Return (cost, witnesses) of the cheapest accepting edited strings.

    cost is None when no string within max_cost is accepted.
    """
    start = tuple(t.type for t in tokens if t.type != EOF_SYMBOL)
    if accepts(start, parser):
        return 0, [start]
    seen = {start}
    frontier = {start}
    for k in range(1, max_cost + 1):
        nxt = set()
        for s in frontier:
            for t in _neighbors(s, alphabet):
                if t not in seen:
                    seen.add(t)
                    nxt.add(t)
        hits = sorted(t for t in nxt if accepts(t, parser))
        if hits:
            return k, hits
        frontier = nxt
    return None, []
