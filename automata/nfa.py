"""Epsilon-NFA with interval-labelled edges and an independent interpreter.

The alphabet and the transition structure are given explicitly; no regular
expression or automata library is used anywhere.  ``simulate`` is the
independent interpreter used to double-check every counterexample produced
by the symbolic (partition-based) machinery.
"""
from __future__ import annotations

import itertools

from .intervals import Interval, partition


class NFA:
    _uid_counter = itertools.count()

    def __init__(self, alphabet: Interval, name: str = "?"):
        self.alphabet = alphabet
        self.name = name
        self.uid = next(NFA._uid_counter)
        # Bumped on every structural mutation; caches key on it.
        self.version = 0
        self.initial: int | None = None
        self.accepts: set[int] = set()
        self.transitions: dict[int, list[tuple[Interval, int]]] = {}
        self.epsilon: dict[int, set[int]] = {}
        self._states: set[int] = set()

    # ------------------------------------------------------------------ build
    def _touch(self) -> None:
        self.version += 1

    def add_state(self, state: int | None = None) -> int:
        if state is None:
            state = max(self._states, default=-1) + 1
        self._states.add(state)
        self._touch()
        return state

    def set_initial(self, state: int) -> None:
        self.add_state(state)
        self.initial = state
        self._touch()

    def add_accept(self, state: int) -> None:
        self.add_state(state)
        self.accepts.add(state)
        self._touch()

    def add_transition(self, src: int, interval: Interval, dst: int) -> int:
        """Add a symbolic edge; returns its index for later modification."""
        self.add_state(src)
        self.add_state(dst)
        self.transitions.setdefault(src, []).append((interval, dst))
        self._touch()
        return len(self.transitions[src]) - 1

    def replace_transition(
        self,
        src: int,
        index: int,
        interval: Interval | None = None,
        dst: int | None = None,
    ) -> None:
        """Modify a single symbolic edge (invalidates derived caches)."""
        old_interval, old_dst = self.transitions[src][index]
        self.transitions[src][index] = (
            old_interval if interval is None else interval,
            old_dst if dst is None else dst,
        )
        self._touch()

    def remove_transition(self, src: int, index: int) -> None:
        del self.transitions[src][index]
        self._touch()

    def add_epsilon(self, src: int, dst: int) -> None:
        self.add_state(src)
        self.add_state(dst)
        self.epsilon.setdefault(src, set()).add(dst)
        self._touch()

    # ------------------------------------------------------------------ query
    def states(self) -> set[int]:
        return set(self._states)

    def edge_intervals(self) -> list[Interval]:
        return [iv for edges in self.transitions.values() for iv, _ in edges]

    def cells(self) -> list[Interval]:
        """Alphabet partition induced by this NFA's edges over its alphabet."""
        return partition(self.edge_intervals(), domain=self.alphabet)

    def epsilon_closure(self, states) -> frozenset[int]:
        """Reflexive-transitive epsilon closure; terminates on epsilon cycles."""
        closure = set(states)
        worklist = list(states)
        while worklist:
            state = worklist.pop()
            for nxt in self.epsilon.get(state, ()):
                if nxt not in closure:
                    closure.add(nxt)
                    worklist.append(nxt)
        return frozenset(closure)

    def subset_step(
        self, subset: frozenset[int], cell: Interval
    ) -> tuple[frozenset[int], tuple[tuple[int, Interval, int], ...]]:
        """One symbolic step of subset construction over a partition cell.

        Missing transitions naturally lead to the empty subset (dead state).
        Returns the successor subset plus the original edges that fired
        (per-step evidence).
        """
        nxt: set[int] = set()
        fired: list[tuple[int, Interval, int]] = []
        for state in subset:
            for interval, dst in self.transitions.get(state, ()):
                if interval.intersects(cell):
                    nxt.add(dst)
                    fired.append((state, interval, dst))
        return self.epsilon_closure(nxt), tuple(fired)

    # ------------------------------------------------- independent interpreter
    def simulate(self, word) -> bool:
        """Independent NFA interpreter: does ``word`` belong to the language?

        Works symbol by symbol directly on the original edges and epsilon
        transitions -- no partitioning, determinization or caching involved.
        Used to independently verify counterexamples.
        """
        if self.initial is None:
            return False
        current = self.epsilon_closure({self.initial})
        for symbol in word:
            nxt: set[int] = set()
            for state in current:
                for interval, dst in self.transitions.get(state, ()):
                    if interval.contains(symbol):
                        nxt.add(dst)
            current = self.epsilon_closure(nxt)
        return bool(current & self.accepts)

    # ------------------------------------------------------------ serialization
    def to_dict(self) -> dict:
        return {
            "name": self.name,
            "alphabet": [self.alphabet.lo, self.alphabet.hi],
            "initial": self.initial,
            "accepts": sorted(self.accepts),
            "transitions": [
                [src, iv.lo, iv.hi, dst]
                for src, edges in sorted(self.transitions.items())
                for iv, dst in edges
            ],
            "epsilon": [
                [src, dst]
                for src, dsts in sorted(self.epsilon.items())
                for dst in sorted(dsts)
            ],
        }

    @classmethod
    def from_dict(cls, data: dict) -> "NFA":
        nfa = cls(Interval(*data["alphabet"]), name=data.get("name", "?"))
        nfa.set_initial(data["initial"])
        for state in data["accepts"]:
            nfa.add_accept(state)
        for src, lo, hi, dst in data["transitions"]:
            nfa.add_transition(src, Interval(lo, hi), dst)
        for src, dst in data["epsilon"]:
            nfa.add_epsilon(src, dst)
        return nfa
