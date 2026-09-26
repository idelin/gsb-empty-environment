"""Lazy (on-demand) subset construction with dependency-tracked caching.

Determinized states (subsets of NFA states) are only computed when the
product-graph search actually visits them.  Each cached entry records the
NFA states it was derived from (``deps``); when an edge is edited, only the
entries depending on the touched states are dropped.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, FrozenSet, Iterable, Tuple

from .nfa import NFA
from .symbols import Interval, partition


@dataclass
class SubsetInfo:
    atoms: Tuple[Interval, ...]              # partition of the universe for this subset
    moves: Dict[Interval, FrozenSet[int]]    # atom -> epsilon-closed target subset
    is_final: bool
    deps: FrozenSet[int]                     # NFA states this entry was derived from


class LazyDFA:
    def __init__(self, nfa: NFA):
        self.nfa = nfa
        self._cache: Dict[FrozenSet[int], SubsetInfo] = {}
        self.computed_states = 0

    def initial(self) -> FrozenSet[int]:
        return self.nfa.eps_closure({self.nfa.initial})

    def _sync(self) -> None:
        if self.nfa.dirty:
            self.invalidate(self.nfa.dirty)
            self.nfa.dirty.clear()

    def invalidate(self, states: Iterable[int]) -> int:
        """Locally drop cache entries derived from any of ``states``."""
        states = set(states)
        doomed = [k for k, info in self._cache.items() if info.deps & states]
        for k in doomed:
            del self._cache[k]
        return len(doomed)

    def info(self, subset: FrozenSet[int]) -> SubsetInfo:
        self._sync()
        got = self._cache.get(subset)
        if got is not None:
            return got
        closure = self.nfa.eps_closure(subset)
        edges = [e for s in closure for e in self.nfa.edges.get(s, ())]
        atoms = partition([e.symbols for e in edges], self.nfa.universe)
        moves: Dict[Interval, FrozenSet[int]] = {}
        for atom in atoms:
            dsts = {e.dst for e in edges if e.symbols.intersects_interval(atom)}
            moves[atom] = self.nfa.eps_closure(dsts)
        info = SubsetInfo(
            atoms=atoms,
            moves=moves,
            is_final=bool(closure & self.nfa.finals),
            deps=closure,
        )
        self._cache[subset] = info
        self.computed_states += 1
        return info

    def cache_size(self) -> int:
        return len(self._cache)
