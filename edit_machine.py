"""Bounded edit-distance state machine built without regex/automaton libraries.

States are explicit.  A normal state (\"n\", i) means: i pattern characters have
been consumed.  A transposition-pending state (\"t\", i) means that pattern
positions i and i+1 were swapped in the text; p[i] must still be consumed.

Transitions (edges are annotated with an explicit cost and edit operation):

* match/substitute: (n,i) --c--> (n,i+1) on text symbol c
* insert:            (n,i) --c--> (n,i)   on text symbol c
* delete:            (n,i) -eps-> (n,i+1)
* transpose start:   (n,i) --c--> (t,i)   when c == p[i+1]
* transpose finish:  (t,i) --c--> (n,i+2) when c == p[i]
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Hashable, Optional, Sequence

GAP = "-"


@dataclass(frozen=True)
class Edit:
    """One aligned edit operation."""

    op: str  # match | substitute | insert | delete | transpose
    text: tuple
    pattern: tuple
    cost: int

    def __str__(self) -> str:
        text = _join(self.text)
        pattern = _join(self.pattern)
        if self.op == "match":
            label = "match"
        elif self.op == "substitute":
            label = f"substitute({text}->{pattern})"
        elif self.op == "insert":
            label = f"insert({text})"
        elif self.op == "delete":
            label = f"delete({pattern})"
        else:
            label = f"transpose({pattern}->{text})"
        return f"{self.cost}:{label}"


@dataclass(frozen=True)
class MatchResult:
    accepted: bool
    distance: Optional[int]
    edits: tuple
    text_alignment: str
    pattern_alignment: str

    def __str__(self) -> str:
        if not self.accepted:
            return "no match (distance > k)"
        lines = [
            f"distance={self.distance}",
            f"  text:    {self.text_alignment}",
            f"  pattern: {self.pattern_alignment}",
            f"  path:    {' '.join(str(e) for e in self.edits)}",
        ]
        return "\n".join(lines)


@dataclass(frozen=True)
class _Node:
    state: Hashable
    cost: int
    raw: tuple  # tuple of RawOp, preserved as a node attribute
    parent: Hashable


@dataclass(frozen=True)
class _RawOp:
    op: str
    text: tuple
    pattern: tuple
    cost: int


def _join(seq: tuple) -> str:
    if all(isinstance(ch, str) and len(ch) == 1 for ch in seq):
        return "".join(seq)
    return " ".join(repr(ch) for ch in seq)


class EditDistanceNFA:
    """Explicit-state edit-distance automaton with explicit edge costs."""

    def __init__(
        self,
        states,
        start,
        accepts,
        symbol_transitions,
        epsilon_transitions,
        alphabet,
        k,
    ) -> None:
        self.states = tuple(states)
        self.start = start
        self.accepts = frozenset(accepts)
        self.symbol_transitions = {
            state: {sym: tuple(targets) for sym, targets in table.items()}
            for state, table in symbol_transitions.items()
        }
        self.epsilon_transitions = {
            state: tuple(targets) for state, targets in epsilon_transitions.items()
        }
        self.alphabet = tuple(alphabet)
        self.k = k
        self._validate()

    def _validate(self) -> None:
        if self.start not in self.states:
            raise ValueError("start state is not in states")
        unknown = self.accepts.difference(self.states)
        if unknown:
            raise ValueError(f"accept states not in states: {set(unknown)}")
        for state in self.states:
            for targets in self.symbol_transitions.get(state, {}).values():
                for target, _cost, _raw in targets:
                    if target not in self.states:
                        raise ValueError(f"symbol transition to unknown state {target!r}")
            for target, _cost, _raw in self.epsilon_transitions.get(state, ()):
                if target not in self.states:
                    raise ValueError(f"epsilon transition to unknown state {target!r}")

    @classmethod
    def build(
        cls,
        pattern: Sequence,
        alphabet: Sequence,
        k: int,
        insertion_cost: int = 1,
        deletion_cost: int = 1,
        substitution_cost: int = 1,
        transposition_cost: Optional[int] = None,
    ) -> "EditDistanceNFA":
        """Construct the machine; alphabet and edge costs are explicit inputs."""
        if k < 0:
            raise ValueError("k must be >= 0")
        for name, cost in (
            ("insertion_cost", insertion_cost),
            ("deletion_cost", deletion_cost),
            ("substitution_cost", substitution_cost),
        ):
            if cost < 0:
                raise ValueError(f"{name} must be >= 0")
        if transposition_cost is not None and transposition_cost < 0:
            raise ValueError("transposition_cost must be >= 0")

        alphabet = tuple(alphabet)
        alphabet_set = frozenset(alphabet)
        if len(alphabet_set) != len(alphabet):
            raise ValueError("alphabet contains duplicate symbols")
        pattern = tuple(pattern)
        for ch in pattern:
            if ch not in alphabet_set:
                raise ValueError(f"pattern symbol {ch!r} not in alphabet")

        n = len(pattern)
        states = [("n", i) for i in range(n + 1)]
        if transposition_cost is not None:
            states += [("t", i) for i in range(max(n - 1, 0))]

        sym = {state: {ch: [] for ch in alphabet} for state in states}
        eps: dict = {state: [] for state in states}

        for i in range(n + 1):
            state = ("n", i)

            for ch in alphabet:
                if i < n:
                    if ch == pattern[i]:
                        edge = (("n", i + 1), 0, _RawOp("match", (ch,), (ch,), 0))
                    else:
                        edge = (
                            ("n", i + 1),
                            substitution_cost,
                            _RawOp("substitute", (ch,), (pattern[i],), substitution_cost),
                        )
                    sym[state][ch].append(edge)

                sym[state][ch].append(
                    (("n", i), insertion_cost, _RawOp("insert", (ch,), (), insertion_cost))
                )

            if i < n:
                eps[state].append(
                    (("n", i + 1), deletion_cost, _RawOp("delete", (), (pattern[i],), deletion_cost))
                )

            if transposition_cost is not None and i + 1 < n:
                a = pattern[i]
                b = pattern[i + 1]
                sym[state][b].append(
                    (
                        ("t", i),
                        transposition_cost,
                        _RawOp("transpose-start", (b,), (a, b), transposition_cost),
                    )
                )

        if transposition_cost is not None:
            for i in range(n - 1):
                a = pattern[i]
                sym[("t", i)][a].append(
                    (("n", i + 2), 0, _RawOp("transpose-end", (a,), (), 0))
                )

        symbol_transitions = {
            state: {ch: tuple(edges) for ch, edges in table.items() if edges}
            for state, table in sym.items()
        }
        epsilon_transitions = {
            state: tuple(edges) for state, edges in eps.items() if edges
        }
        return cls(
            states,
            ("n", 0),
            {("n", n)},
            symbol_transitions,
            epsilon_transitions,
            alphabet,
            k,
        )

    def stream(self):
        return Stream(self)


class Stream:
    """Keeps the set of live (state, optimal-cost) nodes for streaming input."""

    def __init__(self, machine: EditDistanceNFA) -> None:
        self.machine = machine
        self.steps = 0
        start = machine.start
        self.nodes = {start: _Node(start, 0, (), None)}
        self._close()

    def _relax(self, nodes, target, cost, raw, parent):
        node = nodes.get(target)
        if node is None or cost < node.cost:
            nodes[target] = _Node(target, cost, raw, parent)
            return True
        return False

    def _close(self) -> None:
        nodes = self.nodes
        k = self.machine.k
        work = deque(nodes)
        while work:
            state = work.popleft()
            node = nodes[state]
            for target, cost, rawop in self.machine.epsilon_transitions.get(state, ()):
                new_cost = node.cost + cost
                raw = node.raw + ((rawop,) if rawop is not None else ())
                if new_cost <= k and self._relax(nodes, target, new_cost, raw, state):
                    work.append(target)

    def feed(self, symbol) -> "Stream":
        if symbol not in self.machine.alphabet:
            raise ValueError(f"symbol {symbol!r} is outside the declared alphabet")
        self.steps += 1
        k = self.machine.k
        new_nodes: dict = {}
        for state, node in self.nodes.items():
            for target, cost, rawop in self.machine.symbol_transitions.get(state, {}).get(symbol, ()):
                new_cost = node.cost + cost
                if new_cost <= k:
                    raw = node.raw + ((rawop,) if rawop is not None else ())
                    self._relax(new_nodes, target, new_cost, raw, state)
        self.nodes = new_nodes
        self._close()
        return self

    def feed_all(self, symbols) -> "Stream":
        for symbol in symbols:
            self.feed(symbol)
        return self

    @property
    def alive(self) -> bool:
        return bool(self.nodes)

    @property
    def state_costs(self) -> dict:
        return {state: node.cost for state, node in self.nodes.items()}

    def best(self) -> Optional[MatchResult]:
        best_state = None
        for state in self.nodes:
            if state in self.machine.accepts and (
                best_state is None or self.nodes[state].cost < self.nodes[best_state].cost
            ):
                best_state = state
        if best_state is None:
            return None
        node = self.nodes[best_state]
        edits = _collapse(node.raw)
        text_parts = []
        pattern_parts = []
        for edit in edits:
            text_parts.extend(edit.text + (GAP,) * (len(edit.pattern) - len(edit.text)))
            pattern_parts.extend(edit.pattern + (GAP,) * (len(edit.text) - len(edit.pattern)))
        return MatchResult(
            True,
            node.cost,
            tuple(edits),
            _join(tuple(text_parts)),
            _join(tuple(pattern_parts)),
        )

    def result(self) -> MatchResult:
        best = self.best()
        if best is not None:
            return best
        return MatchResult(False, None, (), "", "")


def _collapse(raw: tuple) -> list:
    edits = []
    i = 0
    while i < len(raw):
        op = raw[i]
        if op.op == "transpose-start" and i + 1 < len(raw) and raw[i + 1].op == "transpose-end":
            end = raw[i + 1]
            edits.append(
                Edit(
                    "transpose",
                    op.text + end.text,
                    op.pattern,
                    op.cost + end.cost,
                )
            )
            i += 2
        else:
            edits.append(Edit(op.op, op.text, op.pattern, op.cost))
            i += 1
    return edits


def distance(pattern, text, alphabet, k, **kwargs) -> Optional[int]:
    stream = EditDistanceNFA.build(pattern, alphabet, k, **kwargs).stream()
    stream.feed_all(text)
    result = stream.best()
    return None if result is None else result.distance
