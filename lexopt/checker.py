"""Independent checker: re-validates solver claims against the original model.

This module deliberately shares no evaluation state with the solver. It
re-evaluates every original constraint predicate on the claimed assignment
and recomputes the objective from scratch. For small search spaces it can
also independently confirm optimality or infeasibility by brute-force
enumeration (``itertools.product``), which shares no code with the
solver's branch-and-bound.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product
from typing import Any

from .model import Model
from .solver import INFEASIBLE, OPTIMAL, FEASIBLE, UNKNOWN, SolveResult

DEFAULT_ENUM_LIMIT = 1_000_000


@dataclass(frozen=True)
class CheckReport:
    ok: bool
    problems: tuple[str, ...] = ()
    notes: tuple[str, ...] = ()


def check_solution(
    model: Model,
    assignment: dict[str, Any],
    claimed_objective: tuple[int, ...] | None = None,
) -> CheckReport:
    """Validate an assignment against the original constraints."""
    problems: list[str] = []

    for var in model.variables:
        if var.name not in assignment:
            problems.append(f"variable {var.name!r} is not assigned")
        elif assignment[var.name] not in var.domain:
            problems.append(
                f"variable {var.name!r}={assignment[var.name]!r} is outside "
                f"its domain {var.domain!r}"
            )
    extra = set(assignment) - {v.name for v in model.variables}
    if extra:
        problems.append(f"assignment contains unknown variable(s): {sorted(extra)}")
    if problems:
        return CheckReport(ok=False, problems=tuple(problems))

    objective = [0] * model.num_levels
    for c in model.constraints:
        satisfied = c.predicate(assignment)
        if c.hard:
            if not satisfied:
                problems.append(f"hard constraint {c.name!r} is violated")
        elif not satisfied:
            objective[c.level] += c.weight

    objective_t = tuple(objective)
    if claimed_objective is not None and claimed_objective != objective_t:
        problems.append(
            f"claimed objective {claimed_objective} does not match "
            f"recomputed objective {objective_t}"
        )
    return CheckReport(ok=not problems, problems=tuple(problems))


def brute_force_objectives(model: Model, enum_limit: int = DEFAULT_ENUM_LIMIT):
    """Enumerate all assignments independently; returns (feasible_count, best_objective).

    Raises ValueError if the search space exceeds ``enum_limit``.
    """
    variables = model.variables
    space = 1
    for v in variables:
        space *= len(v.domain)
    if space > enum_limit:
        raise ValueError(
            f"search space of {space} assignments exceeds enum_limit={enum_limit}"
        )
    feasible = 0
    best: tuple[int, ...] | None = None
    names = [v.name for v in variables]
    domains = [v.domain for v in variables]
    for values in product(*domains):
        assignment = dict(zip(names, values))
        if any(c.hard and not c.predicate(assignment) for c in model.constraints):
            continue
        feasible += 1
        obj = [0] * model.num_levels
        for c in model.constraints:
            if not c.hard and not c.predicate(assignment):
                obj[c.level] += c.weight
        obj_t = tuple(obj)
        if best is None or obj_t < best:
            best = obj_t
    return feasible, best


def verify_result(
    model: Model,
    result: SolveResult,
    *,
    enum_limit: int = DEFAULT_ENUM_LIMIT,
) -> CheckReport:
    """Independently verify a SolveResult against the original model."""
    problems: list[str] = []
    notes: list[str] = []

    if result.status in (OPTIMAL, FEASIBLE):
        if result.assignment is None or result.objective is None:
            problems.append(f"status {result.status} but no assignment/objective")
        else:
            report = check_solution(model, result.assignment, result.objective)
            problems.extend(report.problems)
            recomputed_violations = sum(
                1
                for c in model.constraints
                if not c.hard and not c.predicate(result.assignment)
            )
            if recomputed_violations != len(result.violations):
                problems.append(
                    f"result lists {len(result.violations)} violation(s) but "
                    f"independent recount found {recomputed_violations}"
                )
        if result.lower_bound is not None and result.objective is not None:
            if result.lower_bound > result.objective:
                problems.append(
                    f"lower bound {result.lower_bound} exceeds objective "
                    f"{result.objective}, which is impossible"
                )
        if result.status == OPTIMAL:
            try:
                _, brute_best = brute_force_objectives(model, enum_limit)
            except ValueError as exc:
                notes.append(f"optimality not brute-force verified: {exc}")
            else:
                if brute_best != result.objective:
                    problems.append(
                        f"brute-force optimum {brute_best} differs from "
                        f"claimed objective {result.objective}"
                    )
                else:
                    notes.append("optimality confirmed by independent enumeration")
    elif result.status == INFEASIBLE:
        try:
            feasible_count, _ = brute_force_objectives(model, enum_limit)
        except ValueError as exc:
            notes.append(f"infeasibility not brute-force verified: {exc}")
        else:
            if feasible_count != 0:
                problems.append(
                    f"claimed INFEASIBLE but enumeration found "
                    f"{feasible_count} feasible assignment(s)"
                )
            else:
                notes.append("infeasibility confirmed by independent enumeration")
    elif result.status == UNKNOWN:
        notes.append("status UNKNOWN: nothing to verify beyond bound sanity")
    else:
        problems.append(f"unrecognized status {result.status!r}")

    return CheckReport(ok=not problems, problems=tuple(problems), notes=tuple(notes))
