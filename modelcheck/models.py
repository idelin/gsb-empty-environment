"""Sample finite-state models used by the tests and the demo."""
from __future__ import annotations


class ExplicitModel:
    """A finite-state model given by an explicit transition table."""

    def __init__(self, initials, transitions, invariants):
        self._initials = list(initials)
        self._transitions = {state: list(out) for state, out in transitions.items()}
        self._invariants = list(invariants)

    def initial_states(self):
        return list(self._initials)

    def successors(self, state):
        return list(self._transitions.get(state, ()))

    def invariants(self):
        return list(self._invariants)


class MutexModel:
    """N-process mutual exclusion.

    Phases: I (idle) -> W (waiting) -> C (critical) -> I ...
    The buggy version lets a waiting process enter the critical section
    unconditionally; the fixed version only when nobody is critical.
    """

    def __init__(self, n=2, fixed=False):
        self.n = n
        self.fixed = fixed

    def initial_states(self):
        return [tuple("I" * self.n)]

    def successors(self, state):
        out = []
        for i, phase in enumerate(state):
            if phase == "I":
                out.append((f"p{i}.request", state[:i] + ("W",) + state[i + 1:]))
            elif phase == "W":
                if self.fixed and "C" in state:
                    continue
                out.append((f"p{i}.enter", state[:i] + ("C",) + state[i + 1:]))
            else:  # "C"
                out.append((f"p{i}.exit", state[:i] + ("I",) + state[i + 1:]))
        return out

    def invariants(self):
        return [("mutex", lambda s: s.count("C") <= 1)]


class CounterModel:
    """k symmetric counters, each counting 0..max_count.

    States related by permutation are symmetric; pair with
    PermutationNormalizer to explore the (smaller) quotient graph.
    """

    def __init__(self, k=3, max_count=2, sum_limit=None):
        self.k = k
        self.max_count = max_count
        self.sum_limit = sum_limit if sum_limit is not None else k * max_count

    def initial_states(self):
        return [tuple(0 for _ in range(self.k))]

    def successors(self, state):
        out = []
        for i, value in enumerate(state):
            if value < self.max_count:
                out.append((f"c{i}.inc", state[:i] + (value + 1,) + state[i + 1:]))
        return out

    def invariants(self):
        limit = self.sum_limit
        return [("sum-limit", lambda s: sum(s) <= limit)]
