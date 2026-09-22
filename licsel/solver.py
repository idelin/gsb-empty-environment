"""Branch-and-bound solver over license-branch choices on the full graph.

A choice variable exists per package (node) in the dependency graph: the
alternative terms of its license expression (OR branches). Choices are
global - picking a branch constrains transitive dependencies through the
propagation-rule instances, so per-package greedy selection is unsound
in general (see samples/three_package_global.json).

Objective is lexicographic: (1) fewest WITH exceptions used,
(2) fewest distinct packages requiring disclosure. Shared dependencies
are a single node, hence counted once.

Search budget: when the node budget is exhausted the solver reports the
best feasible assignment found so far as an UPPER BOUND with
``complete=False`` - it never claims optimality or full verification.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .expr import Atom
from .model import ConstraintInstance, Problem, compute_instances

STATUS_OPTIMAL = "optimal"
STATUS_FEASIBLE_UPPER_BOUND = "feasible_upper_bound"
STATUS_INFEASIBLE = "infeasible"
STATUS_UNKNOWN = "unknown"


class FixedChoiceError(ValueError):
    """A fixed (pinned) choice is not available for that package."""


class _BudgetExhausted(Exception):
    pass


@dataclass
class SolveResult:
    status: str
    complete: bool
    objective: tuple[int, int] | None  # (exceptions, disclosure packages)
    choices: dict[str, tuple[Atom, ...]]
    disclosures: tuple[str, ...]
    explored: int

    @property
    def pending(self) -> bool:
        return not self.complete

    def choices_as_json(self) -> dict[str, list[dict]]:
        return {
            ref: [
                {"license": atom.license, "exception": atom.exception}
                for atom in term
            ]
            for ref, term in sorted(self.choices.items())
        }


def term_exceptions(term: tuple[Atom, ...]) -> int:
    return sum(1 for atom in term if atom.exception)


def term_has_disclosure(problem: Problem, term: tuple[Atom, ...]) -> bool:
    tags = problem.rules.disclosure_tags
    return any(problem.rules.tags_of(atom.license) & tags for atom in term)


def instance_holds(
    problem: Problem,
    instance: ConstraintInstance,
    assignment: dict[str, tuple[Atom, ...]],
) -> bool:
    """Check one rule instance against a (complete-enough) assignment."""
    rules = problem.rules
    trigger_term = assignment[instance.trigger]
    triggered = any(
        instance.trigger_tag in rules.tags_of(atom.license)
        and not (
            instance.unless_exception is not None
            and atom.exception == instance.unless_exception
        )
        for atom in trigger_term
    )
    if not triggered:
        return True
    for ref in instance.scope_nodes:
        for atom in assignment[ref]:
            if not (rules.tags_of(atom.license) & instance.require_tag_in):
                return False
    return True


def instance_violations(
    problem: Problem,
    instance: ConstraintInstance,
    assignment: dict[str, tuple[Atom, ...]],
) -> list[str]:
    """Human-readable violated members of an instance (assumes triggered)."""
    bad = []
    rules = problem.rules
    for ref in sorted(instance.scope_nodes):
        for atom in assignment[ref]:
            if not (rules.tags_of(atom.license) & instance.require_tag_in):
                bad.append(f"{ref}: {atom}")
    return bad


class Solver:
    def __init__(self, problem: Problem, budget: int = 100_000):
        self.problem = problem
        self.budget = budget
        self.instances = compute_instances(problem)
        self.nodes = sorted(problem.graph.nodes)

    # ------------------------------------------------------------------ api

    def solve(
        self,
        fixed: dict[str, int] | None = None,
        preferred: dict[str, int] | None = None,
    ) -> SolveResult:
        """Solve; ``fixed``/``preferred`` map ref -> term index."""
        fixed = dict(fixed or {})
        preferred = dict(preferred or {})
        problem = self.problem

        order = sorted(
            self.nodes,
            key=lambda r: (r not in fixed, len(problem.graph.nodes[r].terms), r),
        )
        cand_order: dict[str, list[int]] = {}
        for ref in self.nodes:
            terms = problem.graph.nodes[ref].terms
            cands = list(range(len(terms)))
            pref = preferred.get(ref)
            cands.sort(
                key=lambda i: (
                    i != pref,
                    term_exceptions(terms[i]),
                    term_has_disclosure(problem, terms[i]),
                    i,
                )
            )
            if ref in fixed:
                idx = fixed[ref]
                if not 0 <= idx < len(terms):
                    raise FixedChoiceError(
                        f"fixed choice #{idx} unavailable for {ref}"
                    )
                cand_order[ref] = [idx]
            else:
                cand_order[ref] = cands

        assignment: dict[str, tuple[Atom, ...]] = {}
        best: list = [None]  # [objective, assignment]
        state = {"explored": 0, "exhausted": False}

        def fully_assigned_instances_hold() -> bool:
            assigned = assignment.keys()
            for inst in self.instances:
                if inst.involved <= assigned and not instance_holds(
                    problem, inst, assignment
                ):
                    return False
            return True

        def dfs(k: int, exc: int, disc: frozenset[str]) -> None:
            if state["explored"] >= self.budget:
                state["exhausted"] = True
                raise _BudgetExhausted
            if best[0] is not None and (exc, len(disc)) >= best[0][0]:
                return  # monotone objective: cannot strictly improve
            if k == len(order):
                best[0] = ((exc, len(disc)), dict(assignment))
                return
            ref = order[k]
            terms = problem.graph.nodes[ref].terms
            for idx in cand_order[ref]:
                state["explored"] += 1
                term = terms[idx]
                assignment[ref] = term
                if fully_assigned_instances_hold():
                    next_disc = disc
                    if term_has_disclosure(problem, term):
                        next_disc = disc | {ref}
                    dfs(k + 1, exc + term_exceptions(term), next_disc)
                del assignment[ref]

        try:
            dfs(0, 0, frozenset())
        except _BudgetExhausted:
            pass

        explored = state["explored"]
        exhausted = state["exhausted"]
        if best[0] is None:
            status = STATUS_UNKNOWN if exhausted else STATUS_INFEASIBLE
            return SolveResult(status, not exhausted, None, {}, (), explored)
        objective, chosen = best[0]
        disclosures = tuple(
            sorted(
                ref
                for ref, term in chosen.items()
                if term_has_disclosure(problem, term)
            )
        )
        status = STATUS_FEASIBLE_UPPER_BOUND if exhausted else STATUS_OPTIMAL
        return SolveResult(status, not exhausted, objective, chosen, disclosures, explored)


# ------------------------------------------------------------- brute force

def enumerate_optimal(problem: Problem) -> tuple[tuple[int, int] | None, int]:
    """Exhaustively enumerate all choice combinations (small graphs only).

    Returns (best objective or None, combinations checked). Used to
    cross-validate the branch-and-bound solver on small graphs.
    """
    from itertools import product

    instances = compute_instances(problem)
    nodes = sorted(problem.graph.nodes)
    terms_per_node = [problem.graph.nodes[ref].terms for ref in nodes]
    best: tuple[int, int] | None = None
    checked = 0
    for combo in product(*terms_per_node):
        checked += 1
        assignment = dict(zip(nodes, combo))
        if not all(
            instance_holds(problem, inst, assignment) for inst in instances
        ):
            continue
        exc = sum(term_exceptions(term) for term in combo)
        disc = sum(
            1 for term in combo if term_has_disclosure(problem, term)
        )
        if best is None or (exc, disc) < best:
            best = (exc, disc)
    return best, checked
