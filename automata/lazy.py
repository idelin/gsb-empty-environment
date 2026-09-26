"""Lazy automata views: determinization, products, complement.

Everything is computed on demand over partition cells; the (potentially
exponential) state space is only materialized where a search actually
steps.  All views expose the same interface:

    cells()              -> list[Interval]   partition of the alphabet
    initial()            -> hashable state
    is_accept(state)     -> bool
    step(state, cell)    -> (next_state, evidence)
"""
from __future__ import annotations

from .intervals import Interval, partition
from .nfa import NFA

# Evidence entry: (automaton_name, src_state, edge_interval, dst_state)
Evidence = tuple[tuple[str, int, Interval, int], ...]


class SubsetConstruction:
    """Lazy determinization of an NFA (epsilon-closure subsets).

    Missing transitions complete to the empty subset, i.e. an implicit
    dead state, so the determinized automaton is total.  The per-cell step
    cache is local to this view and is dropped when the underlying NFA is
    mutated (its version changes) -- stale inclusion conclusions can never
    be reused.
    """

    def __init__(self, nfa: NFA, cells: list[Interval] | None = None):
        self.nfa = nfa
        self._cells = list(cells) if cells is not None else nfa.cells()
        self._step_cache: dict = {}
        self._cache_version = nfa.version

    def cells(self) -> list[Interval]:
        return self._cells

    def initial(self) -> frozenset[int]:
        return self.nfa.epsilon_closure({self.nfa.initial})

    def is_accept(self, state: frozenset[int]) -> bool:
        return bool(state & self.nfa.accepts)

    def step(self, state: frozenset[int], cell: Interval):
        if self.nfa.version != self._cache_version:
            # Local invalidation: only this automaton's cache is dropped.
            self._step_cache.clear()
            self._cache_version = self.nfa.version
        key = (state, cell)
        if key not in self._step_cache:
            self._step_cache[key] = self.nfa.subset_step(state, cell)
        nxt, fired = self._step_cache[key]
        evidence = tuple((self.nfa.name, src, iv, dst) for src, iv, dst in fired)
        return nxt, evidence


class Product:
    """On-demand product graph of two lazy automata.

    ``mode`` is one of "inter" (intersection), "union", "diff"
    (difference, left \\ right).  The alphabet partitions of both operands
    are first unified into a common refinement, so operands with
    inconsistent interval boundaries compose correctly.
    """

    MODES = ("inter", "union", "diff")

    def __init__(self, left, right, mode: str):
        if mode not in self.MODES:
            raise ValueError(f"unknown product mode {mode!r}")
        self.left = left
        self.right = right
        self.mode = mode
        self._cells = partition(list(left.cells()) + list(right.cells()))

    def cells(self) -> list[Interval]:
        return self._cells

    def initial(self):
        return (self.left.initial(), self.right.initial())

    def is_accept(self, state) -> bool:
        left_accept = self.left.is_accept(state[0])
        right_accept = self.right.is_accept(state[1])
        if self.mode == "inter":
            return left_accept and right_accept
        if self.mode == "union":
            return left_accept or right_accept
        return left_accept and not right_accept

    def step(self, state, cell: Interval):
        left_next, left_ev = self.left.step(state[0], cell)
        right_next, right_ev = self.right.step(state[1], cell)
        return (left_next, right_next), left_ev + right_ev


class Complement:
    """Complement of a deterministic lazy automaton (e.g. SubsetConstruction).

    Sound because SubsetConstruction is total: missing transitions were
    already completed to the dead state, which becomes accepting here.
    """

    def __init__(self, base):
        self.base = base

    def cells(self) -> list[Interval]:
        return self.base.cells()

    def initial(self):
        return self.base.initial()

    def is_accept(self, state) -> bool:
        return not self.base.is_accept(state)

    def step(self, state, cell: Interval):
        nxt, evidence = self.base.step(state, cell)
        return nxt, evidence


def run_word(automaton, word) -> bool:
    """Run a lazy automaton on a concrete word (membership test)."""
    state = automaton.initial()
    for symbol in word:
        cell = next((c for c in automaton.cells() if c.contains(symbol)), None)
        if cell is None:
            raise ValueError(f"symbol {symbol!r} outside the alphabet domain")
        state, _ = automaton.step(state, cell)
    return automaton.is_accept(state)
