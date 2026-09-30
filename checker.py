"""Independent solution checker.

Deliberately kept separate from the solver: it re-evaluates the ORIGINAL
domains and constraints exactly as the user supplied them (no normalised
rows, no propagated domains), so a bug inside the solver cannot make an
invalid assignment look valid.  The solver calls this before accepting or
returning any incumbent.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Mapping, Optional


@dataclass(frozen=True)
class CheckReport:
    ok: bool
    objective: Optional[int]
    violations: tuple = ()

    def __str__(self) -> str:
        if self.ok:
            return f"CheckReport(ok=True, objective={self.objective})"
        return f"CheckReport(ok=False, violations={list(self.violations)})"


def check_assignment(problem, assignment: Mapping) -> CheckReport:
    """Verify `assignment` against the original problem data.

    Checks, independently of the solver internals:
      * every declared variable is assigned exactly one value;
      * no undeclared variables are present;
      * every value is an int inside the variable's original finite domain;
      * every original constraint holds under direct evaluation;
      * the objective value is recomputed from scratch.
    """
    violations = []
    names = problem.variable_names

    for name in names:
        if name not in assignment:
            violations.append(f"missing assignment for variable {name!r}")
    for name in assignment:
        if name not in problem.domains:
            violations.append(f"assignment contains unknown variable {name!r}")

    for name in names:
        if name not in assignment:
            continue
        value = assignment[name]
        if isinstance(value, bool) or not isinstance(value, int):
            violations.append(f"{name!r} has non-integer value {value!r}")
        elif value not in problem.domains[name]:
            violations.append(
                f"{name}={value} is outside its domain "
                f"{sorted(problem.domains[name])}")

    complete = all(n in assignment for n in names)
    if complete:
        for idx, con in enumerate(problem.constraints):
            total = sum(coef * assignment[name] for name, coef in con.coeffs)
            if con.sense == "<=":
                holds = total <= con.rhs
            elif con.sense == ">=":
                holds = total >= con.rhs
            else:  # "=="
                holds = total == con.rhs
            if not holds:
                violations.append(
                    f"constraint #{idx} violated: lhs={total} "
                    f"does not satisfy {con.sense} {con.rhs}")

    objective = None
    if complete and all(isinstance(assignment[n], int) and
                        not isinstance(assignment[n], bool) for n in names):
        objective = sum(problem.objective.get(n, 0) * assignment[n] for n in names)

    return CheckReport(ok=not violations, objective=objective,
                       violations=tuple(violations))
