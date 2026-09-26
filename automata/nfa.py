"""Explicitly-defined epsilon-NFA over integer-interval symbol sets.

The alphabet (``universe``) and the full transition structure are given
explicitly by the caller; no regular-expression or automata library is used.
Every structural mutation bumps ``version`` and records the touched source
states in ``dirty`` so that caches can be invalidated locally.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Iterable, List, Optional, Set

from .symbols import SymbolSet


@dataclass(frozen=True)
class Edge:
    id: int
    src: int
    symbols: SymbolSet
    dst: int


@dataclass(frozen=True)
class EpsilonEdge:
    id: int
    src: int
    dst: int


class NFA:
    def __init__(
        self,
        universe: SymbolSet,
        num_states: int,
        initial: int,
        finals: Iterable[int] = (),
        name: str = "",
    ):
        self.universe = universe if isinstance(universe, SymbolSet) else SymbolSet(universe)
        self.num_states = num_states
        self.initial = initial
        self.finals = frozenset(finals)
        self.name = name
        self.edges: Dict[int, List[Edge]] = {}
        self.eps_edges: Dict[int, List[EpsilonEdge]] = {}
        self.version = 0
        self.dirty: Set[int] = set()
        self._next_id = 0
        self._check_state(initial)
        for f in self.finals:
            self._check_state(f)

    # -- structure ---------------------------------------------------------
    def _check_state(self, state: int) -> None:
        if not 0 <= state < self.num_states:
            raise ValueError(f"state {state} out of range 0..{self.num_states - 1}")

    def _touch(self, *states: int) -> None:
        self.version += 1
        self.dirty.update(states)

    def add_edge(self, src: int, symbols, dst: int) -> Edge:
        self._check_state(src)
        self._check_state(dst)
        if not isinstance(symbols, SymbolSet):
            symbols = SymbolSet(symbols)
        if symbols.is_empty():
            raise ValueError("edge with empty symbol set")
        edge = Edge(self._next_id, src, symbols, dst)
        self._next_id += 1
        self.edges.setdefault(src, []).append(edge)
        self._touch(src)
        return edge

    def add_epsilon(self, src: int, dst: int) -> EpsilonEdge:
        self._check_state(src)
        self._check_state(dst)
        edge = EpsilonEdge(self._next_id, src, dst)
        self._next_id += 1
        self.eps_edges.setdefault(src, []).append(edge)
        self._touch(src)
        return edge

    def remove_edge(self, edge_id: int) -> bool:
        for src, lst in self.edges.items():
            for i, e in enumerate(lst):
                if e.id == edge_id:
                    del lst[i]
                    self._touch(src)
                    return True
        return False

    def replace_edge_symbols(self, edge_id: int, symbols) -> Edge:
        """Modify the symbol set of one existing edge in place."""
        if not isinstance(symbols, SymbolSet):
            symbols = SymbolSet(symbols)
        if symbols.is_empty():
            raise ValueError("edge with empty symbol set")
        for src, lst in self.edges.items():
            for i, e in enumerate(lst):
                if e.id == edge_id:
                    new_edge = Edge(e.id, src, symbols, e.dst)
                    lst[i] = new_edge
                    self._touch(src)
                    return new_edge
        raise KeyError(f"no edge with id {edge_id}")

    def edge_by_id(self, edge_id: int) -> Optional[Edge]:
        for lst in self.edges.values():
            for e in lst:
                if e.id == edge_id:
                    return e
        return None

    # -- semantics ---------------------------------------------------------
    def eps_closure(self, states: Iterable[int]) -> frozenset:
        """Epsilon closure; tolerates epsilon cycles."""
        seen: Set[int] = set(states)
        stack = list(seen)
        while stack:
            s = stack.pop()
            for e in self.eps_edges.get(s, ()):
                if e.dst not in seen:
                    seen.add(e.dst)
                    stack.append(e.dst)
        return frozenset(seen)


def accepts(nfa: NFA, word: Iterable[int]) -> bool:
    """Independent interpreter: simulate ``nfa`` on ``word`` directly.

    Deliberately separate from the determinization/inclusion machinery so
    counterexamples can be re-checked against the original NFA.  Symbols
    outside the declared alphabet (or without any outgoing edge) fall into
    the implicit dead state.
    """
    current = nfa.eps_closure({nfa.initial})
    for sym in word:
        if not nfa.universe.contains(sym):
            current = frozenset()
            continue
        nxt: Set[int] = set()
        for s in current:
            for e in nfa.edges.get(s, ()):
                if e.symbols.contains(sym):
                    nxt.add(e.dst)
        current = nfa.eps_closure(nxt)
    return bool(current & nfa.finals)
