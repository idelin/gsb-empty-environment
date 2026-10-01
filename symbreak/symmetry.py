"""Symmetry declaration and lexicographic-representative constraints.

Two entry points:

* ``declare_exchangeable`` -- the user declares a group of variables to be
  fully exchangeable.  Every adjacent transposition is first *proven* to
  be an automorphism of the original constraints and objective (equal
  domains and untouched special roles are checked as well); only then are
  ordering constraints ``x1 <= x2 <= ... <= xn`` added.  Any violation
  raises ``SymmetryRejected`` carrying the minimal counterexample.

* ``add_symmetry_generators`` -- the user supplies arbitrary generator
  permutations.  Each generator is verified as an automorphism first
  (otherwise ``SymmetryRejected`` with the minimal counterexample and no
  constraint is added).  The group closure is then built under a budget
  and one lex-leader-style constraint ``X <=lex pi(X)`` is added per
  non-identity element.

In both cases only *proven* symmetries ever produce constraints, and
variables that were never declared symmetric are never ordered.
"""

from __future__ import annotations

from math import factorial

from .core import Constraint, Problem
from .groups import closure
from .permutations import (
    Counterexample,
    Perm,
    identity,
    is_identity,
    key,
    normalize,
    verify_automorphism,
)


class SymmetryRejected(Exception):
    """A declared symmetry failed verification; carries the counterexample."""

    def __init__(self, counterexample: Counterexample):
        super().__init__(counterexample.describe())
        self.counterexample = counterexample


def _transposition(variables, a, b) -> Perm:
    return {v: (b if v == a else a if v == b else v) for v in variables}


def declare_exchangeable(problem: Problem, group_vars) -> tuple:
    """Declare ``group_vars`` fully exchangeable; add ordering constraints.

    Returns ``(added_constraints, group_elements)`` where ``group_elements``
    is the full symmetric group on the declared variables (usable to
    recover symmetric solutions).
    """
    group_vars = list(group_vars)
    if len(group_vars) < 2:
        raise ValueError("an exchangeable group needs at least two variables")
    unknown = [v for v in group_vars if v not in problem.domains]
    if unknown:
        raise ValueError(f"unknown variables in group: {unknown}")
    if len(set(group_vars)) != len(group_vars):
        raise ValueError("duplicate variables in group")

    variables = problem.variables
    transpositions = [
        normalize(_transposition(variables, group_vars[i], group_vars[i + 1]), variables)
        for i in range(len(group_vars) - 1)
    ]
    # Verify first; add constraints only if every transposition is proven.
    for perm in transpositions:
        cx = verify_automorphism(problem, perm)
        if cx is not None:
            raise SymmetryRejected(cx)

    added = []
    for i in range(len(group_vars) - 1):
        lo, hi = group_vars[i], group_vars[i + 1]
        con = Constraint(
            name=f"lex_order[{lo}<={hi}]",
            scope=(lo, hi),
            predicate=lambda a, b: a <= b,
        )
        added.append(con)
    problem.symmetry_constraints.extend(added)

    group = closure(transpositions, variables, budget=factorial(len(group_vars)))
    return added, group


def add_symmetry_generators(problem: Problem, generators, budget: int = 1000) -> tuple:
    """Verify generator permutations, close them under ``budget``, and add
    one lex constraint ``X <=lex pi(X)`` per non-identity element.

    Returns ``(added_constraints, group_elements)``.
    """
    variables = problem.variables
    perms = [normalize(g, variables) for g in generators]
    # Verify every generator before adding anything (atomicity).
    for perm in perms:
        cx = verify_automorphism(problem, perm)
        if cx is not None:
            raise SymmetryRejected(cx)

    group = closure(perms, variables, budget=budget)

    added = []
    for perm in group:
        if is_identity(perm):
            continue
        scope = tuple(v for v in variables if perm[v] != v)
        idx = tuple(scope.index(perm[v]) for v in scope)
        con = Constraint(
            name=(
                "lex_le[" + ",".join(scope)
                + "<=" + ",".join(perm[v] for v in scope) + "]"
            ),
            scope=scope,
            predicate=lambda *vals, idx=idx: vals <= tuple(vals[i] for i in idx),
        )
        added.append(con)
    problem.symmetry_constraints.extend(added)
    return added, group


def merge_groups(problem: Problem, groups, budget: int = 100000) -> list:
    """Closure of the union of already-proven group elements (for recovery
    when several independent symmetries were declared)."""
    variables = problem.variables
    gens = []
    seen = set()
    for group in groups:
        for perm in group:
            k = key(perm, variables)
            if k not in seen and not is_identity(perm):
                seen.add(k)
                gens.append(perm)
    if not gens:
        return [identity(variables)]
    return closure(gens, variables, budget=budget)
