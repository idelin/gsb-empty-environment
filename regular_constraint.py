"""Regular constraint: a variable sequence must be accepted by a DFA.

Enforces arc consistency on the sequence ``X_0 .. X_{n-1}`` with finite
domains by combining, for every position ``i``:

* forward reachable states  ``fwd[i]``  -- states reachable after reading
  the first ``i`` symbols, and
* backward live states      ``bwd[i]``  -- states from which the suffix
  ``X_i .. X_{n-1}`` can still reach an accepting state.

A value ``v`` is supported at position ``i`` iff some state
``q in fwd[i]`` has ``delta(q, v) in bwd[i+1]``.  Values without support
only lead to dead paths and are removed.  Supports incremental domain
updates (only the affected layers are recomputed) and empty sequences
(satisfiable iff the start state is accepting).

No SAT/SMT solver is used; everything is explicit set propagation.
"""

from __future__ import annotations

from typing import Hashable, Iterable, List, Optional, Sequence, Set

from automaton import DFA


class RegularConstraint:
    """Arc-consistency propagator for ``regular([X_0..X_{n-1}], dfa)``."""

    def __init__(self, dfa: DFA, domains: Sequence[Iterable[Hashable]]) -> None:
        self.dfa = dfa
        self.domains: List[Set[Hashable]] = [set(d) for d in domains]
        for i, dom in enumerate(self.domains):
            if not dom <= dfa.alphabet:
                raise ValueError(
                    f"domain of position {i} is not a subset of the alphabet"
                )
        # Snapshot of the domains as originally posted; the independent
        # checker validates any claimed solution against these.
        self.original_domains: List[Set[Hashable]] = [set(d) for d in self.domains]
        self.n = len(self.domains)
        self._fwd: List[Set[Hashable]] = [set() for _ in range(self.n + 1)]
        self._bwd: List[Set[Hashable]] = [set() for _ in range(self.n + 1)]
        self._recompute_forward(0)
        self._recompute_backward(self.n)

    # ------------------------------------------------------------------
    # reachable-set maintenance
    # ------------------------------------------------------------------
    def _recompute_forward(self, from_layer: int) -> None:
        """Recompute ``fwd[from_layer..n]`` (layer 0 is the start state)."""
        if from_layer == 0:
            self._fwd[0] = {self.dfa.start}
            from_layer = 1
        for i in range(from_layer, self.n + 1):
            reach: Set[Hashable] = set()
            for state in self._fwd[i - 1]:
                for value in self.domains[i - 1]:
                    reach.add(self.dfa.step(state, value))
            self._fwd[i] = reach

    def _recompute_backward(self, from_layer: int) -> None:
        """Recompute ``bwd[from_layer..0]`` (layer n is the accepting set)."""
        if from_layer == self.n:
            self._bwd[self.n] = set(self.dfa.accepting)
            from_layer = self.n - 1
        for i in range(from_layer, -1, -1):
            live: Set[Hashable] = set()
            for state in self.dfa.states:
                for value in self.domains[i]:
                    if self.dfa.step(state, value) in self._bwd[i + 1]:
                        live.add(state)
                        break
            self._bwd[i] = live

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def is_satisfiable(self) -> bool:
        """True iff some word over the current domains is accepted.

        For an empty sequence (``n == 0``) this reduces to whether the
        start state is accepting.
        """
        return bool(self._fwd[self.n] & self.dfa.accepting)

    def supported_values(self, i: int) -> Set[Hashable]:
        """Values at position ``i`` that lie on some accepting path."""
        supported: Set[Hashable] = set()
        for state in self._fwd[i]:
            for value in self.domains[i]:
                if self.dfa.step(state, value) in self._bwd[i + 1]:
                    supported.add(value)
        return supported

    def filter(self) -> bool:
        """Prune every domain to its supported values (arc consistency).

        Returns False iff the constraint became unsatisfiable.
        """
        for i in range(self.n):
            self.domains[i] = self.supported_values(i)
        self._recompute_forward(1 if self.n else 0)
        self._recompute_backward(self.n - 1 if self.n else 0)
        return self.is_satisfiable()

    def witness(self) -> Optional[List[Hashable]]:
        """Build one accepted word greedily, or None if unsatisfiable."""
        if not self.is_satisfiable():
            return None
        word: List[Hashable] = []
        state = self.dfa.start
        for i in range(self.n):
            for value in sorted(self.domains[i], key=repr):
                nxt = self.dfa.step(state, value)
                if nxt in self._bwd[i + 1]:
                    word.append(value)
                    state = nxt
                    break
            else:  # pragma: no cover - unreachable when satisfiable
                raise AssertionError("no live successor on a satisfiable path")
        return word

    # ------------------------------------------------------------------
    # incremental updates
    # ------------------------------------------------------------------
    def remove_value(self, i: int, value: Hashable) -> bool:
        """Remove ``value`` from domain ``i``; recompute affected layers only.

        Layers ``fwd[i+1..n]`` and ``bwd[i..0]`` depend on domain ``i`` and
        are refreshed; all other layers are untouched.  Returns the new
        satisfiability status.
        """
        if value in self.domains[i]:
            self.domains[i].discard(value)
            self._recompute_forward(i + 1)
            self._recompute_backward(i)
        return self.is_satisfiable()

    def set_domain(self, i: int, values: Iterable[Hashable]) -> bool:
        """Replace domain ``i`` (e.g. assignment); recompute affected layers."""
        new_domain = set(values)
        if not new_domain <= self.dfa.alphabet:
            raise ValueError("domain is not a subset of the alphabet")
        self.domains[i] = new_domain
        self._recompute_forward(i + 1)
        self._recompute_backward(i)
        return self.is_satisfiable()
