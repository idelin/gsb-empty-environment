"""Internal finite-domain solver with an explicit node budget.

Plain depth-first backtracking over finite domains (no SAT/SMT library).
Three-valued result:

- ``Status.SAT``: a witness assignment was found.
- ``Status.UNSAT``: the whole search space was explored, no witness exists.
- ``Status.UNKNOWN``: the node budget ran out before the search finished.

``UNKNOWN`` is a budget artifact, not a logical answer. Callers must never
treat it as SAT or UNSAT (e.g. to keep shrinking a conflict core).
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Callable, Mapping, Optional, Sequence


class Status(Enum):
    SAT = "SAT"
    UNSAT = "UNSAT"
    UNKNOWN = "UNKNOWN"


@dataclass(frozen=True)
class Constraint:
    """A named predicate over a fixed tuple of variables."""

    name: str
    variables: tuple[str, ...]
    predicate: Callable[..., bool]

    def holds(self, assignment: Mapping[str, int]) -> bool:
        return bool(self.predicate(*(assignment[v] for v in self.variables)))


@dataclass(frozen=True)
class Problem:
    """Finite-domain variables plus the original constraint list."""

    domains: Mapping[str, tuple[int, ...]]
    constraints: tuple[Constraint, ...]

    def __post_init__(self) -> None:
        for con in self.constraints:
            for var in con.variables:
                if var not in self.domains:
                    raise ValueError(f"{con.name}: unknown variable {var!r}")
        for var, dom in self.domains.items():
            if not dom:
                raise ValueError(f"variable {var!r} has an empty domain")


@dataclass
class SolveResult:
    status: Status
    witness: Optional[dict] = None
    nodes: int = 0
    budget: int = 0


class _BudgetExhausted(Exception):
    pass


def solve(
    problem: Problem,
    indices: Optional[Sequence[int]] = None,
    budget: int = 10_000,
) -> SolveResult:
    """Solve the sub-problem formed by ``indices`` of the original constraints.

    ``budget`` caps the number of tried variable assignments (search nodes).
    UNSAT is returned only after the search space is provably exhausted.
    """
    if indices is None:
        cons = list(problem.constraints)
    else:
        cons = [problem.constraints[i] for i in indices]
    variables = list(problem.domains)
    assignment: dict = {}
    nodes = 0

    def partial_ok() -> bool:
        assigned = assignment.keys()
        for con in cons:
            if all(v in assigned for v in con.variables) and not con.holds(assignment):
                return False
        return True

    def backtrack(pos: int) -> Optional[dict]:
        nonlocal nodes
        if pos == len(variables):
            return dict(assignment)
        var = variables[pos]
        for value in problem.domains[var]:
            nodes += 1
            if nodes > budget:
                raise _BudgetExhausted
            assignment[var] = value
            if partial_ok():
                found = backtrack(pos + 1)
                if found is not None:
                    return found
            del assignment[var]
        return None

    try:
        witness = backtrack(0)
    except _BudgetExhausted:
        return SolveResult(Status.UNKNOWN, None, nodes, budget)
    if witness is None:
        return SolveResult(Status.UNSAT, None, nodes, budget)
    return SolveResult(Status.SAT, witness, nodes, budget)
