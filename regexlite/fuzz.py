"""Cross-validation of the engines against the reference backtracker.

Strategy: enumerate *all* strings up to a small length over a small explicit
alphabet and compare, for each string,

* the Pike VM (tags included) against the independent reference backtracker
  -- they must agree on (start, end, tags) exactly;
* the lazy DFA (with a deliberately tiny cache, to exercise eviction)
  against the same extent.

Any disagreement is returned as a :class:`Mismatch` which
:func:`write_reproducer` turns into a standalone, independently executable
Python script.
"""
from __future__ import annotations

import itertools
import random
from dataclasses import dataclass

from .ast_nodes import (
    Alt, CharClass, Concat, Dot, Empty, Group, Literal, Node, Star,
)
from .dfa import Determinizer
from .nfa import build_nfa
from .parser import parse
from .pike import pike_search
from .reference import Inconclusive, ref_search

_META = set("|*()[]\\^-.")


@dataclass
class Mismatch:
    pattern: str
    alphabet: str
    string: str
    expected: object  # reference result
    got: object       # pike result


# -- pattern generation ----------------------------------------------------

def random_ast(rng: random.Random, alphabet: str, depth: int) -> Node:
    """Random small AST (spans are meaningless here; the serializer reparses)."""
    span = (0, 0)
    choices = ["lit", "lit", "class", "dot", "empty"]
    if depth > 0:
        choices += ["concat", "alt", "star", "group", "group"]
    kind = rng.choice(choices)
    if kind == "lit":
        return Literal(span, rng.choice(alphabet))
    if kind == "class":
        lo, hi = sorted(rng.sample(alphabet, 2))
        ranges = ((lo, hi),) if rng.random() < 0.7 else ((lo, lo), (hi, hi))
        return CharClass(span, ranges, rng.random() < 0.3)
    if kind == "dot":
        return Dot(span)
    if kind == "empty":
        return Empty(span)
    if kind == "concat":
        parts = tuple(random_ast(rng, alphabet, depth - 1)
                      for _ in range(rng.randint(1, 3)))
        return Concat(span, parts)
    if kind == "alt":
        branches = tuple(random_ast(rng, alphabet, depth - 1)
                         for _ in range(rng.randint(2, 3)))
        return Alt(span, branches)
    if kind == "star":
        return Star(span, random_ast(rng, alphabet, depth - 1))
    # group (unnamed: labels are reassigned deterministically on reparse)
    return Group(span, "?", random_ast(rng, alphabet, depth - 1))


def _escape(char: str) -> str:
    return "\\" + char if char in _META else char


def to_pattern(node: Node, ctx: int = 0) -> str:
    """Serialize an AST back to a pattern string (round-trips the parser)."""
    if isinstance(node, Empty):
        return ""
    if isinstance(node, Literal):
        return _escape(node.char)
    if isinstance(node, CharClass):
        body = "".join(lo if lo == hi else f"{lo}-{hi}" for lo, hi in node.ranges)
        return "[" + ("^" if node.negated else "") + body + "]"
    if isinstance(node, Dot):
        return "."
    if isinstance(node, Concat):
        s = "".join(to_pattern(p, 2) for p in node.parts)
        return f"({s})" if 2 < ctx else s
    if isinstance(node, Alt):
        s = "|".join(to_pattern(b, 1) for b in node.branches)
        return f"({s})" if 1 < ctx else s
    if isinstance(node, Star):
        child = node.child
        atomic = isinstance(child, (Literal, CharClass, Dot, Group))
        return (to_pattern(child, 0) if atomic else f"({to_pattern(child, 0)})") + "*"
    if isinstance(node, Group):
        return f"({to_pattern(node.child, 0)})"
    raise TypeError(node)  # pragma: no cover


# -- string enumeration ------------------------------------------------------

def all_strings(alphabet: str, max_len: int):
    for length in range(max_len + 1):
        for tup in itertools.product(alphabet, repeat=length):
            yield "".join(tup)


# -- comparison ----------------------------------------------------------------

def _dfa_search(det: Determinizer, text: str):
    """Leftmost-longest extent via the lazy DFA; returns (start, end) | None."""
    for start in range(len(text) + 1):
        state = det.start()
        last = None
        pos = start
        while True:
            if state.accepting:
                last = pos
            if pos >= len(text):
                break
            nxt = det.step(state, text[pos])
            if nxt is None:
                break
            state, pos = nxt, pos + 1
        if last is not None:
            return (start, last)
    return None


def check_string(pattern: str, alphabet: str, s: str):
    """Compare all engines on one string; returns Mismatch | None."""
    parsed = parse(pattern)
    nfa = build_nfa(parsed, alphabet)
    expected = ref_search(parsed, alphabet, s)
    got = pike_search(nfa, s)
    if got != expected:
        return Mismatch(pattern, alphabet, s, expected, got)
    det = Determinizer(nfa, cache_size=2, state_budget=4096)
    extent = _dfa_search(det, s)
    want = None if expected is None else (expected[0], expected[1])
    if extent != want:
        return Mismatch(pattern, alphabet, s, want, extent)
    return None


def check_pattern(pattern: str, alphabet: str, max_len: int):
    """Exhaustively check one pattern; returns Mismatch | None.

    Raises nothing on Inconclusive reference runs; those strings are skipped
    and counted in the returned tuple's second element when no mismatch.
    """
    skipped = 0
    for s in all_strings(alphabet, max_len):
        try:
            mm = check_string(pattern, alphabet, s)
        except Inconclusive:
            skipped += 1
            continue
        if mm is not None:
            return mm, skipped
    return None, skipped


# -- reproducer ------------------------------------------------------------------

_REPRO_TEMPLATE = '''#!/usr/bin/env python3
"""Auto-generated counterexample reproducer for regexlite.

Independently executable:  python3 {name}
Exits 0 when the engines agree, 1 when the recorded (or a fresh)
cross-check still shows a mismatch.
"""
import sys

sys.path.insert(0, {root!r})  # repository root containing the regexlite package

from regexlite import build_nfa, parse, pike_search, ref_search

PATTERN = {pattern!r}
ALPHABET = {alphabet!r}
STRING = {string!r}
RECORDED_EXPECTED = {expected!r}  # reference backtracker result when reported
RECORDED_GOT = {got!r}            # pike VM result when reported


def main() -> int:
    parsed = parse(PATTERN)
    nfa = build_nfa(parsed, ALPHABET)
    expected = ref_search(parsed, ALPHABET, STRING)
    got = pike_search(nfa, STRING)
    print(f"pattern={{PATTERN!r}} alphabet={{ALPHABET!r}} string={{STRING!r}}")
    print(f"reference: {{expected}}")
    print(f"pike vm  : {{got}}")
    bad = (got != expected) or (RECORDED_EXPECTED != RECORDED_GOT)
    print("MISMATCH" if bad else "engines agree")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
'''


def write_reproducer(mm: Mismatch, directory: str, root: str, name: str | None = None) -> str:
    """Write a standalone reproducer script for ``mm``; returns its path."""
    import os
    name = name or "repro_counterexample.py"
    path = os.path.join(directory, name)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(_REPRO_TEMPLATE.format(
            name=name, root=root, pattern=mm.pattern, alphabet=mm.alphabet,
            string=mm.string, expected=mm.expected, got=mm.got))
    return path
