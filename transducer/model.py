"""Explicit finite-state transducer (FST) model.

Everything is spelled out by hand: the alphabet, the state set and the
transition list are given explicitly by the caller.  No regular-expression
or automata libraries are used anywhere in this package.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Transition:
    """One transducer edge.

    edge_id:      unique identifier; used as the final tie-breaker and as
                  the backbone of path evidence.
    src, dst:     endpoint states.
    input_symbol: a single character from the alphabet, or None for an
                  epsilon edge (consumes no input).
    output:       string emitted when the edge fires (may be "").
    cost:         non-negative real number.
    """

    edge_id: int
    src: str
    dst: str
    input_symbol: str | None
    output: str
    cost: float

    @property
    def is_epsilon(self) -> bool:
        return self.input_symbol is None

    @property
    def is_silent(self) -> bool:
        """Consumes no input and emits no output."""
        return self.input_symbol is None and self.output == ""


class FST:
    """A weighted finite-state transducer with non-negative edge costs."""

    def __init__(self, states, alphabet, transitions, start, accepts):
        states = frozenset(states)
        alphabet = frozenset(alphabet)
        accepts = frozenset(accepts)
        transitions = tuple(transitions)

        if not states:
            raise ValueError("states must be non-empty")
        if not all(isinstance(s, str) and s for s in states):
            raise ValueError("every state must be a non-empty string")
        if not all(isinstance(c, str) and len(c) == 1 for c in alphabet):
            raise ValueError("alphabet symbols must be single characters")
        if start not in states:
            raise ValueError(f"unknown start state {start!r}")
        if not accepts <= states:
            raise ValueError("accept states must be a subset of states")

        by_id: dict[int, Transition] = {}
        adj: dict[str, list[Transition]] = {s: [] for s in states}
        for tr in transitions:
            if not isinstance(tr, Transition):
                raise TypeError("transitions must be Transition objects")
            if tr.edge_id in by_id:
                raise ValueError(f"duplicate edge id {tr.edge_id}")
            if tr.src not in states or tr.dst not in states:
                raise ValueError(f"edge {tr.edge_id} references an unknown state")
            if tr.input_symbol is not None and tr.input_symbol not in alphabet:
                raise ValueError(
                    f"edge {tr.edge_id} uses symbol {tr.input_symbol!r} "
                    "outside the alphabet"
                )
            if not isinstance(tr.output, str):
                raise TypeError(f"edge {tr.edge_id}: output must be a string")
            if isinstance(tr.cost, bool) or not isinstance(tr.cost, (int, float)):
                raise TypeError(f"edge {tr.edge_id}: cost must be a real number")
            if not tr.cost >= 0:
                raise ValueError(f"edge {tr.edge_id}: negative cost {tr.cost}")
            by_id[tr.edge_id] = tr
            adj[tr.src].append(tr)
        for edges in adj.values():
            edges.sort(key=lambda t: t.edge_id)

        self.states = states
        self.alphabet = alphabet
        self.start = start
        self.accepts = accepts
        self.transitions = tuple(sorted(by_id.values(), key=lambda t: t.edge_id))
        self._adj = adj
        self._by_id = by_id

    def out_edges(self, state: str) -> list[Transition]:
        return self._adj[state]

    def edge(self, edge_id: int) -> Transition:
        return self._by_id[edge_id]

    def check_text(self, text: str) -> None:
        """Validate that every input symbol belongs to the alphabet."""
        bad = sorted({c for c in text if c not in self.alphabet})
        if bad:
            raise ValueError(f"input symbols outside the alphabet: {bad}")

    def find_zero_cost_silent_cycle(self) -> list[int] | None:
        """Return one zero-cost silent cycle (edge ids, a closed walk) or None.

        Such a cycle is exactly why an unnormalised "minimum by edge-id
        sequence" need not exist: traversing the cycle more times keeps cost
        and output fixed while the edge-id tuple can keep decreasing, so a
        naive minimum does not exist.  The search layer normalises these
        cycles away; this diagnostic makes the phenomenon inspectable.
        """
        adj: dict[str, list[Transition]] = {s: [] for s in self.states}
        for tr in self.transitions:
            if tr.is_silent and tr.cost == 0:
                adj[tr.src].append(tr)

        WHITE, GRAY, BLACK = 0, 1, 2
        color = {s: WHITE for s in self.states}
        stack: list[tuple[str, Transition]] = []  # (state, edge taken from it)

        def dfs(u: str) -> list[int] | None:
            color[u] = GRAY
            for tr in adj[u]:
                v = tr.dst
                if color[v] == GRAY:
                    cyc = [tr.edge_id]
                    for s, e in reversed(stack):
                        cyc.append(e.edge_id)
                        if s == v:
                            break
                    return list(reversed(cyc))
                if color[v] == WHITE:
                    stack.append((u, tr))
                    found = dfs(v)
                    if found is not None:
                        return found
                    stack.pop()
            color[u] = BLACK
            return None

        for s in sorted(self.states):
            if color[s] == WHITE:
                found = dfs(s)
                if found is not None:
                    return found
        return None
