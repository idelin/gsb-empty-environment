"""Brute-force finite-domain solver plus an independent solution checker.

The solver enumerates assignments with backtracking over the original
constraints *and* any proven symmetry (lex-representative) constraints.
The independent checker re-validates a claimed solution against the
original constraints only, through a separate code path.
"""

from __future__ import annotations

from .core import Problem


def solve_all(problem: Problem, include_symmetry: bool = True, limit: int | None = None) -> list:
    constraints = list(problem.constraints)
    if include_symmetry:
        constraints += problem.symmetry_constraints
    variables = problem.variables
    # Constraints to re-check once variable at position i is assigned.
    watchers = [[] for _ in variables]
    for con in constraints:
        last = max(variables.index(v) for v in con.scope)
        watchers[last].append(con)

    out = []
    partial = {}

    def backtrack(i: int) -> None:
        if limit is not None and len(out) >= limit:
            return
        if i == len(variables):
            out.append(dict(partial))
            return
        var = variables[i]
        for value in problem.domains[var]:
            partial[var] = value
            if all(con.holds(partial) for con in watchers[i]):
                backtrack(i + 1)
            del partial[var]

    backtrack(0)
    return out


def solve_one(problem: Problem, include_symmetry: bool = True):
    sols = solve_all(problem, include_symmetry=include_symmetry, limit=1)
    return sols[0] if sols else None


def optimize(problem: Problem, maximize: bool = True, include_symmetry: bool = True):
    """Return ``(best_assignment, best_value)`` or ``(None, None)``."""
    if problem.objective is None:
        raise ValueError("problem has no objective")
    best = None
    best_val = None
    for sol in solve_all(problem, include_symmetry=include_symmetry):
        val = problem.objective(sol)
        if best is None or (val > best_val if maximize else val < best_val):
            best, best_val = sol, val
    return best, best_val


def check_original(problem: Problem, assignment) -> bool:
    """Independent checker: validates against the ORIGINAL constraints only.

    Used whenever the solver claims satisfiability, so a bug in the
    symmetry-breaking layer cannot manufacture a false "SAT".
    """
    if set(assignment) != set(problem.variables):
        return False
    for var, val in assignment.items():
        if val not in problem.domains[var]:
            return False
    return all(con.holds(assignment) for con in problem.constraints)
