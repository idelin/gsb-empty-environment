"""Independent reference implementations used to validate the AC-3 solver.

Deliberately written in the simplest possible style (no queue, no cache, no
incrementality) so it shares no code with ac3.CSP and can serve as a
cross-check oracle.  Standard library only; no SAT/SMT solvers.
"""

from __future__ import annotations

from typing import Any, Callable, Hashable

Predicate = Callable[[Any, Any], bool]


def reference_ac(domains: dict, constraints: list) -> dict:
    """Full arc consistency by naive fixpoint iteration.

    ``constraints`` is a list of ``(a, b, pred)`` triples where ``pred``
    takes the values of ``a`` and ``b`` in that order.
    """
    doms = {v: set(d) for v, d in domains.items()}
    changed = True
    while changed:
        changed = False
        for a, b, pred in constraints:
            for x, y, forward in ((a, b, True), (b, a, False)):
                for value in list(doms[x]):
                    ok = any(
                        pred(value, w) if forward else pred(w, value)
                        for w in doms[y]
                    )
                    if not ok:
                        doms[x].discard(value)
                        changed = True
    return doms


def enumerate_solutions(domains: dict, constraints: list, limit: int | None = None) -> list:
    """Enumerate all complete assignments satisfying every constraint."""
    order = sorted(domains, key=lambda v: len(domains[v]))
    solutions: list[dict] = []
    assignment: dict = {}

    def consistent(var: Hashable, value: Any) -> bool:
        for a, b, pred in constraints:
            if a == var and b in assignment and not pred(value, assignment[b]):
                return False
            if b == var and a in assignment and not pred(assignment[a], value):
                return False
        return True

    def backtrack(i: int) -> None:
        if limit is not None and len(solutions) >= limit:
            return
        if i == len(order):
            solutions.append(dict(assignment))
            return
        var = order[i]
        for value in sorted(domains[var], key=repr):
            if consistent(var, value):
                assignment[var] = value
                backtrack(i + 1)
                del assignment[var]

    backtrack(0)
    return solutions


def check_solution(assignment: dict, constraints: list) -> bool:
    """Independent checker: verify a claimed solution against the original
    constraints."""
    return all(pred(assignment[a], assignment[b]) for a, b, pred in constraints)
