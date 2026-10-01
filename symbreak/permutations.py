"""Permutations over variables and exact automorphism verification.

A permutation is represented as a dict mapping every variable to its
image.  It acts on assignments by ``(pi . a)[pi(v)] = a[v]``.

``verify_automorphism`` checks, exactly and over the finite domains, that
a permutation preserves every original constraint and the objective.
When it fails it returns the lexicographically *smallest* counterexample
assignment (over the affected scope) together with the constraint that
is not preserved.
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import product

from .core import Problem

Perm = dict


def identity(variables) -> Perm:
    return {v: v for v in variables}


def normalize(mapping, variables) -> Perm:
    """Fill in unspecified variables as fixed and require a bijection."""
    unknown = set(mapping) - set(variables)
    if unknown:
        raise ValueError(f"permutation mentions unknown variables: {sorted(unknown)}")
    perm = {v: mapping.get(v, v) for v in variables}
    if sorted(perm.values()) != sorted(variables):
        raise ValueError("mapping is not a bijection on the variables")
    return perm


def compose(p: Perm, q: Perm) -> Perm:
    """Composition: ``compose(p, q)[v] == p[q[v]]`` (q applied first)."""
    return {v: p[q[v]] for v in q}


def inverse(p: Perm) -> Perm:
    return {img: v for v, img in p.items()}


def apply_perm(perm: Perm, assignment) -> dict:
    """The assignment pi . a with (pi . a)[pi(v)] = a[v]."""
    return {perm[v]: val for v, val in assignment.items()}


def moved(perm: Perm) -> frozenset:
    return frozenset(v for v, img in perm.items() if v != img)


def key(perm: Perm, variables) -> tuple:
    return tuple(perm[v] for v in variables)


def is_identity(perm: Perm) -> bool:
    return all(v == img for v, img in perm.items())


@dataclass(frozen=True)
class Counterexample:
    """Witness that a permutation is NOT an automorphism."""

    kind: str  # 'special_moved' | 'domain_mismatch' | 'constraint' | 'objective'
    variable: str | None = None
    constraint: str | None = None
    assignment: tuple | None = None  # sorted (var, value) pairs, lex-minimal
    before: object = None
    after: object = None

    def describe(self) -> str:
        if self.kind == "special_moved":
            return (
                f"special-role variable {self.variable!r} is moved to "
                f"{self.after!r}; special roles may not be permuted"
            )
        if self.kind == "domain_mismatch":
            return (
                f"variable {self.variable!r} (domain {self.before!r}) maps to "
                f"{self.after!r} with a different domain"
            )
        who = (
            f"constraint {self.constraint!r}"
            if self.kind == "constraint"
            else f"objective {self.constraint!r}"
        )
        return (
            f"{who} is not preserved: at assignment {dict(self.assignment)!r} "
            f"it evaluates to {self.before!r} but to {self.after!r} "
            f"after applying the permutation"
        )


def _check_static_requirements(problem: Problem, perm: Perm) -> Counterexample | None:
    for var in sorted(problem.special):
        if perm[var] != var:
            return Counterexample(
                "special_moved", variable=var, after=perm[var]
            )
    for var in problem.variables:
        if problem.domains[var] != problem.domains[perm[var]]:
            return Counterexample(
                "domain_mismatch",
                variable=var,
                before=problem.domains[var],
                after=perm[var],
            )
    return None


def verify_automorphism(problem: Problem, perm: Perm) -> Counterexample | None:
    """Return None iff ``perm`` provably preserves all original constraints
    and the objective; otherwise return the lexicographically smallest
    counterexample assignment (across all constraints and the objective).
    """
    static = _check_static_requirements(problem, perm)
    if static is not None:
        return static

    inv = inverse(perm)
    best: Counterexample | None = None
    best_key: tuple | None = None

    def consider(cx: Counterexample, key: tuple):
        nonlocal best, best_key
        if best_key is None or key < best_key:
            best, best_key = cx, key

    for con in problem.constraints:
        affected = sorted(
            set(con.scope)
            | {perm[v] for v in con.scope}
            | {inv[v] for v in con.scope}
        )
        doms = [sorted(problem.domains[v]) for v in affected]
        for values in product(*doms):
            asn = dict(zip(affected, values))
            before = con.holds(asn)
            after = con.holds(apply_perm(perm, asn))
            if before != after:
                k = tuple(sorted(asn.items()))
                consider(
                    Counterexample(
                        "constraint",
                        constraint=con.name,
                        assignment=k,
                        before=before,
                        after=after,
                    ),
                    k,
                )
                break  # first hit in enumeration order is minimal for this constraint

    if problem.objective is not None:
        variables = problem.variables
        doms = [sorted(problem.domains[v]) for v in variables]
        for values in product(*doms):
            asn = dict(zip(variables, values))
            before = problem.objective(asn)
            after = problem.objective(apply_perm(perm, asn))
            if before != after:
                k = tuple(sorted(asn.items()))
                consider(
                    Counterexample(
                        "objective",
                        constraint=problem.objective_name,
                        assignment=k,
                        before=before,
                        after=after,
                    ),
                    k,
                )
                break

    return best
