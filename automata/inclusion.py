"""Language inclusion A (subseteq) B via emptiness of the difference.

A (subseteq) B  iff  L(A) \\ L(B) is empty.  The difference is the
on-demand product of the two lazily determinized NFAs over a *unified*
alphabet partition (the two alphabets may have inconsistent boundaries).
Every counterexample is re-checked by the independent NFA interpreter
before being reported.
"""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Interval, partition
from .lazy import Product, SubsetConstruction
from .nfa import NFA
from .search import EmptinessSearch, Status, Witness

INCLUDED = "included"
NOT_INCLUDED = "not_included"
UNKNOWN = "unknown"


def unified_cells(a: NFA, b: NFA) -> list[Interval]:
    """Common refinement of both alphabets and both edge partitions."""
    domain = Interval(
        min(a.alphabet.lo, b.alphabet.lo), max(a.alphabet.hi, b.alphabet.hi)
    )
    return partition(a.edge_intervals() + b.edge_intervals(), domain=domain)


def verify_counterexample(a: NFA, b: NFA, word) -> bool:
    """Independent check: the word must be accepted by A and rejected by B."""
    return a.simulate(word) and not b.simulate(word)


@dataclass
class InclusionResult:
    status: str                  # INCLUDED | NOT_INCLUDED | UNKNOWN
    witness: Witness | None = None
    verified: bool | None = None  # independent-interpreter confirmation
    states_explored: int = 0

    def __str__(self) -> str:
        if self.status is NOT_INCLUDED or self.status == NOT_INCLUDED:
            return f"not_included(witness={self.witness.symbols!r}, verified={self.verified})"
        return f"{self.status}(states={self.states_explored})"


def check_inclusion(a: NFA, b: NFA, state_budget: int = 100_000) -> InclusionResult:
    """Decide L(a) (subseteq) L(b); UNKNOWN if the budget is exhausted."""
    cells = unified_cells(a, b)
    det_a = SubsetConstruction(a, cells)
    det_b = SubsetConstruction(b, cells)
    difference = Product(det_a, det_b, "diff")
    search = EmptinessSearch(difference, state_budget=state_budget)
    search.run()
    if search.status is Status.NONEMPTY:
        witness = search.witness
        verified = verify_counterexample(a, b, witness.symbols)
        return InclusionResult(
            NOT_INCLUDED, witness=witness, verified=verified,
            states_explored=search.states_explored,
        )
    if search.status is Status.EMPTY:
        return InclusionResult(INCLUDED, states_explored=search.states_explored)
    return InclusionResult(UNKNOWN, states_explored=search.states_explored)


class InclusionCache:
    """Caches inclusion verdicts, keyed by automaton identity + version.

    Mutating any edge bumps the NFA's version, so a modified automaton
    misses the cache and is re-decided -- stale conclusions are never
    reused.  The underlying SubsetConstruction views additionally keep
    per-automaton step caches that are invalidated locally (only the
    modified side is recomputed).
    """

    def __init__(self):
        self._results: dict = {}
        self._views: dict = {}
        self.hits = 0
        self.misses = 0

    def _view(self, nfa: NFA, cells: list[Interval]) -> SubsetConstruction:
        key = (nfa.uid, nfa.version)
        view = self._views.get(key)
        if view is None or view._cells != cells:
            view = SubsetConstruction(nfa, cells)
            self._views[key] = view
        return view

    def check(self, a: NFA, b: NFA, state_budget: int = 100_000) -> InclusionResult:
        key = (a.uid, a.version, b.uid, b.version, state_budget)
        if key in self._results:
            self.hits += 1
            return self._results[key]
        self.misses += 1
        cells = unified_cells(a, b)
        difference = Product(self._view(a, cells), self._view(b, cells), "diff")
        search = EmptinessSearch(difference, state_budget=state_budget)
        search.run()
        if search.status is Status.NONEMPTY:
            witness = search.witness
            result = InclusionResult(
                NOT_INCLUDED, witness=witness,
                verified=verify_counterexample(a, b, witness.symbols),
                states_explored=search.states_explored,
            )
        elif search.status is Status.EMPTY:
            result = InclusionResult(INCLUDED, states_explored=search.states_explored)
        else:
            result = InclusionResult(UNKNOWN, states_explored=search.states_explored)
        self._results[key] = result
        return result
