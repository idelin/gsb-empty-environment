"""Independent backtracking reference matcher.

Deliberately shares no code with the Pike VM / DFA: it walks the AST
recursively and enumerates complete paths in priority order.  It exists so
the fuzzer can cross-check the fast engines against a second, obviously
simple implementation on exhaustively enumerated small strings.

Termination rule for stars: an iteration that matches the empty string is
*forbidden* (skipped).  Recursion into the loop only happens on progress, so
nested stars like ``(a*)*`` cannot spin forever, and the rule coincides with
the Pike VM's epsilon-cycle cut.

Pathological patterns can enumerate exponentially many paths; a visit
counter raises :class:`Inconclusive` so the fuzzer can skip those cases
instead of hanging.
"""
from __future__ import annotations

from .ast_nodes import (
    Alt, CharClass, Concat, Dot, Empty, Group, Literal, Node, Star,
)
from .nfa import class_symbols
from .parser import Parsed

_VISIT_LIMIT = 300_000


class Inconclusive(Exception):
    """The reference matcher refuses to finish (too many paths)."""


def _gen(node: Node, parsed: Parsed, text: str, pos: int, tags: tuple,
         alphabet: frozenset, counter: list):
    """Yield ``(end, tags)`` for every match of ``node`` at ``pos``, in
    priority order (ordered alternation, greedy star)."""
    counter[0] += 1
    if counter[0] > _VISIT_LIMIT:
        raise Inconclusive(f"reference matcher exceeded {_VISIT_LIMIT} visits")

    if isinstance(node, Empty):
        yield pos, tags
    elif isinstance(node, Literal):
        if pos < len(text) and text[pos] == node.char:
            yield pos + 1, tags
    elif isinstance(node, (CharClass, Dot)):
        symbols = alphabet if isinstance(node, Dot) else class_symbols(node, alphabet)
        if pos < len(text) and text[pos] in symbols:
            yield pos + 1, tags
    elif isinstance(node, Concat):
        def rec(k: int, p: int, t: tuple):
            if k == len(node.parts):
                yield p, t
            else:
                for p2, t2 in _gen(node.parts[k], parsed, text, p, t, alphabet, counter):
                    yield from rec(k + 1, p2, t2)
        yield from rec(0, pos, tags)
    elif isinstance(node, Alt):
        for branch in node.branches:
            yield from _gen(branch, parsed, text, pos, tags, alphabet, counter)
    elif isinstance(node, Star):
        child = node.child

        def rec(p: int, t: tuple):
            for p2, t2 in _gen(child, parsed, text, p, t, alphabet, counter):
                if p2 == p:
                    continue  # empty iteration forbidden: no infinite loops
                yield from rec(p2, t2)  # greedy: more iterations first
                yield p2, t2            # then exit after this iteration
            yield p, t                  # zero iterations

        yield from rec(pos, tags)
    elif isinstance(node, Group):
        idx = parsed.labels[node.label]
        begin = list(tags)
        begin[2 * idx] = pos
        for p, t in _gen(node.child, parsed, text, pos, tuple(begin), alphabet, counter):
            end = list(t)
            end[2 * idx + 1] = p
            yield p, tuple(end)
    else:  # pragma: no cover - defensive
        raise TypeError(f"unknown node {node!r}")


def ref_search(parsed: Parsed, alphabet, text: str):
    """Leftmost-longest search; returns ``(start, end, tags)`` or None.

    Among all paths reaching the maximal end at the leftmost start, the one
    enumerated first (highest priority) wins.
    """
    tags0 = (None,) * (2 * parsed.ngroups)
    counter = [0]
    alphabet = frozenset(alphabet)
    for start in range(len(text) + 1):
        best_end = None
        best_tags = None
        for end, tags in _gen(parsed.ast, parsed, text, start, tags0, alphabet, counter):
            if best_end is None or end > best_end:
                best_end, best_tags = end, tags
        if best_end is not None:
            return (start, best_end, best_tags)
    return None
