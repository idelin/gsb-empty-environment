"""On-demand (lazy) determinization of the tagged NFA via subset construction.

The DFA is used to decide *match extents* (does a path accept, and what is
the longest accepting prefix); capture tags are intentionally not part of the
subset state -- captures are recovered by re-simulating the winning span with
the Pike VM, which keeps the determinizer simple and its states few.

Resource accounting, two independent knobs:

* ``cache_size`` -- how many materialized DFA states the LRU cache holds.
  Eviction only drops the cached object; the subset identity is remembered,
  so an evicted state is transparently rebuilt on demand.  Eviction can
  never change results, only the amount of recomputation.
* ``state_budget`` -- hard cap on the number of *distinct* subsets this
  determinizer will ever create.  Hitting it raises :class:`BudgetExceeded`
  and flips :attr:`Determinizer.status` to ``"incomplete"``: the caller is
  told the automaton is unfinished instead of being handed a silently
  truncated (wrong) one.
"""
from __future__ import annotations

from collections import OrderedDict
from dataclasses import dataclass, field

from .nfa import NFA


class BudgetExceeded(Exception):
    """The determinization state budget was exhausted."""


@dataclass
class DFAState:
    sid: int
    subset: frozenset[int]
    accepting: bool
    trans: dict[str, frozenset[int] | None] = field(default_factory=dict)


class Determinizer:
    def __init__(self, nfa: NFA, *, cache_size: int = 256, state_budget: int = 1024):
        if cache_size < 1:
            raise ValueError("cache_size must be >= 1")
        if state_budget < 1:
            raise ValueError("state_budget must be >= 1")
        self.nfa = nfa
        self.cache_size = cache_size
        self.state_budget = state_budget
        self._cache: OrderedDict[frozenset[int], DFAState] = OrderedDict()
        self._distinct: set[frozenset[int]] = set()
        self._counter = 0
        self.exceeded = False
        self.materializations = 0  # includes rebuilds after eviction

    # -- introspection ---------------------------------------------------
    @property
    def status(self) -> str:
        return "incomplete" if self.exceeded else "complete"

    @property
    def distinct_states(self) -> int:
        return len(self._distinct)

    @property
    def cached_states(self) -> int:
        return len(self._cache)

    # -- construction ----------------------------------------------------
    def _eps_closure(self, states) -> frozenset[int]:
        out = set(states)
        stack = list(states)
        while stack:
            state = stack.pop()
            for target, _slot in self.nfa.eps[state]:
                if target not in out:
                    out.add(target)
                    stack.append(target)
        return frozenset(out)

    def _materialize(self, subset: frozenset[int]) -> DFAState:
        state = self._cache.get(subset)
        if state is not None:
            self._cache.move_to_end(subset)
            return state
        if subset not in self._distinct:
            if len(self._distinct) >= self.state_budget:
                self.exceeded = True
                raise BudgetExceeded(
                    f"determinization needs more than {self.state_budget} "
                    f"distinct states; automaton is incomplete")
            self._distinct.add(subset)
        self._counter += 1
        self.materializations += 1
        state = DFAState(self._counter, subset, self.nfa.accept in subset)
        self._cache[subset] = state
        while len(self._cache) > self.cache_size:
            self._cache.popitem(last=False)  # evict LRU; safe: rebuildable
        return state

    def start(self) -> DFAState:
        return self._materialize(self._eps_closure({self.nfa.start}))

    def step(self, state: DFAState, symbol: str) -> DFAState | None:
        """Advance on ``symbol``; None means the dead (no-transition) state."""
        if symbol in state.trans:
            subset = state.trans[symbol]
        else:
            nxt = set()
            for nfa_state in state.subset:
                target = self.nfa.sym[nfa_state].get(symbol)
                if target is not None:
                    nxt.add(target)
            subset = (self._materialize(self._eps_closure(nxt)).subset
                      if nxt else None)
            state.trans[symbol] = subset
        if subset is None:
            return None
        return self._materialize(subset)
