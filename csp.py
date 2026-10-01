"""Finite-domain CSP model with table constraints, plus an independent
solution checker. Standard library only."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Constraint:
    """A table constraint: the tuple of values taken by `scope` (in order)
    must be a member of `table`."""

    scope: tuple[str, ...]
    table: frozenset[tuple]

    def allows(self, assignment: dict) -> bool:
        return tuple(assignment[v] for v in self.scope) in self.table


class CSP:
    def __init__(self, domains: dict[str, tuple | list | set | frozenset],
                 constraints: list[Constraint]):
        self.domains = {v: frozenset(vals) for v, vals in domains.items()}
        self.constraints = list(constraints)
        for c in self.constraints:
            if len(set(c.scope)) != len(c.scope):
                raise ValueError(f"repeated variable in scope {c.scope}")
            for v in c.scope:
                if v not in self.domains:
                    raise ValueError(f"unknown variable {v!r} in constraint")
            for t in c.table:
                if len(t) != len(c.scope):
                    raise ValueError(f"tuple {t} does not match scope {c.scope}")

    def is_satisfied(self, assignment: dict) -> bool:
        return all(c.allows(assignment) for c in self.constraints)


def has_support(constraint: Constraint, domains: dict[str, set],
                var: str, value) -> bool:
    """True iff `var=value` appears in some allowed tuple whose other
    components all lie in the *current* domains."""
    scope = constraint.scope
    idx = scope.index(var)
    for tup in constraint.table:
        if tup[idx] != value:
            continue
        if all(tup[j] in domains[scope[j]] for j in range(len(scope))):
            return True
    return False


def check_solution(csp: CSP, assignment: dict) -> tuple[bool, str]:
    """Independent solution checker, run against the ORIGINAL problem.

    A claimed SAT answer must be a complete assignment (every variable,
    nothing extra), each value must come from the variable's original
    domain, and every original constraint must hold.
    """
    missing = set(csp.domains) - set(assignment)
    extra = set(assignment) - set(csp.domains)
    if missing:
        return False, f"incomplete assignment, missing: {sorted(missing)}"
    if extra:
        return False, f"unknown variables in assignment: {sorted(extra)}"
    for v, val in assignment.items():
        if val not in csp.domains[v]:
            return False, f"{v}={val!r} not in original domain {sorted(csp.domains[v])}"
    for i, c in enumerate(csp.constraints):
        if not c.allows(assignment):
            return False, f"constraint #{i} over {c.scope} violated"
    return True, "ok"
