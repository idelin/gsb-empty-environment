"""Tagged epsilon-NFA over an explicitly supplied alphabet.

Construction is Thompson-style with two twists:

* transitions are stored per explicit alphabet symbol (no intervals, no
  library automata); the alphabet is an input, not an assumption;
* capture boundaries are tag operations on epsilon edges: crossing the edge
  stamps ``tags[slot] = current input position``.  Group ``i`` uses slots
  ``2*i`` (begin) and ``2*i+1`` (end).

Every NFA state records ``origins[state]``: the span of the pattern source
that created it, so automaton structure keeps source positions.

Alternation emits epsilon edges in branch order and Star emits its
"enter the loop" edge before its "exit" edge; simulation consumes edges in
this stored order, which is what makes the priority rules deterministic.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .ast_nodes import (
    Alt, CharClass, Concat, Dot, Empty, Group, Literal, Node, Span, Star,
)
from .parser import Parsed


class CompileError(Exception):
    def __init__(self, message: str, span: Span):
        self.span = span
        super().__init__(f"{message} (pattern offset {span[0]})")


def class_symbols(node: CharClass, alphabet: frozenset[str]) -> frozenset[str]:
    """Resolve a CharClass node to an explicit symbol set."""
    out = {c for lo, hi in node.ranges for c in alphabet if lo <= c <= hi}
    if node.negated:
        out = set(alphabet) - out
    return frozenset(out)


@dataclass
class NFA:
    alphabet: frozenset[str]
    eps: list[list[tuple[int, int | None]]]  # (target, tag slot | None), ordered
    sym: list[dict[str, int]]                # explicit symbol -> target
    origins: list[Span | None]               # source span per state
    start: int
    accept: int
    labels: dict[str, int]                   # capture label -> group index
    nslots: int

    @property
    def nstates(self) -> int:
        return len(self.eps)


class _Builder:
    def __init__(self, alphabet, labels):
        self.alphabet = frozenset(alphabet)
        self.labels = labels
        self.eps: list[list[tuple[int, int | None]]] = []
        self.sym: list[dict[str, int]] = []
        self.origins: list[Span | None] = []

    def state(self, origin: Span | None) -> int:
        self.eps.append([])
        self.sym.append({})
        self.origins.append(origin)
        return len(self.eps) - 1

    def build(self, node: Node) -> tuple[int, int]:
        """Return (entry, exit) states for the fragment of ``node``."""
        span = node.span
        if isinstance(node, Empty):
            s, t = self.state(span), self.state(span)
            self.eps[s].append((t, None))
            return s, t
        if isinstance(node, Literal):
            if node.char not in self.alphabet:
                raise CompileError(
                    f"character {node.char!r} is not in the alphabet", span)
            s, t = self.state(span), self.state(span)
            self.sym[s][node.char] = t
            return s, t
        if isinstance(node, (CharClass, Dot)):
            symbols = (self.alphabet if isinstance(node, Dot)
                       else class_symbols(node, self.alphabet))
            s, t = self.state(span), self.state(span)
            for c in sorted(symbols):
                self.sym[s][c] = t
            # An empty symbol set leaves t unreachable: the empty language.
            return s, t
        if isinstance(node, Concat):
            entry = self.state(span)
            prev = entry
            for part in node.parts:
                e0, e1 = self.build(part)
                self.eps[prev].append((e0, None))
                prev = e1
            return entry, prev
        if isinstance(node, Alt):
            s, t = self.state(span), self.state(span)
            for branch in node.branches:  # edge order == priority order
                e0, e1 = self.build(branch)
                self.eps[s].append((e0, None))
                self.eps[e1].append((t, None))
            return s, t
        if isinstance(node, Star):
            s, t = self.state(span), self.state(span)
            e0, e1 = self.build(node.child)
            self.eps[s].append((e0, None))  # greedy: prefer another iteration
            self.eps[s].append((t, None))   # ... over exiting the loop
            self.eps[e1].append((s, None))
            return s, t
        if isinstance(node, Group):
            idx = self.labels[node.label]
            s, t = self.state(span), self.state(span)
            e0, e1 = self.build(node.child)
            self.eps[s].append((e0, 2 * idx))       # begin tag
            self.eps[e1].append((t, 2 * idx + 1))  # end tag
            return s, t
        raise CompileError(f"cannot compile node {node!r}", span)


def build_nfa(parsed: Parsed, alphabet) -> NFA:
    """Compile a parsed pattern into a tagged epsilon-NFA."""
    if not alphabet:
        raise CompileError("alphabet must be non-empty", (0, 0))
    builder = _Builder(alphabet, parsed.labels)
    entry, exit_ = builder.build(parsed.ast)
    return NFA(
        alphabet=builder.alphabet,
        eps=builder.eps,
        sym=builder.sym,
        origins=builder.origins,
        start=entry,
        accept=exit_,
        labels=dict(parsed.labels),
        nslots=2 * len(parsed.labels),
    )
