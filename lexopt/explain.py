"""Human-readable explanation of penalties and certified lower bounds."""

from __future__ import annotations

from .model import Model
from .solver import SolveResult


def explain(model: Model, result: SolveResult) -> str:
    lines: list[str] = []
    lines.append(f"status: {result.status}")
    lines.append(f"nodes explored: {result.nodes_explored}")

    if result.assignment is None:
        if result.status == "INFEASIBLE":
            lines.append(
                "no assignment satisfies all hard constraints; "
                "penalties cannot buy through hard conflicts"
            )
        if result.lower_bound is not None:
            lines.append(f"best known lower bound (per level): {result.lower_bound}")
        return "\n".join(lines)

    assignment_str = ", ".join(
        f"{name}={result.assignment[name]!r}"
        for name in sorted(result.assignment)
    )
    lines.append(f"assignment: {assignment_str}")
    lines.append(
        f"objective per level (level 0 is highest priority): {result.objective}"
    )

    for level in range(len(result.objective or ())):
        total = result.objective[level]
        lines.append(f"level {level}: total penalty = {total}")
        level_violations = [v for v in result.violations if v.level == level]
        if not level_violations:
            lines.append("  no soft constraint violated at this level")
        for v in level_violations:
            lines.append(f"  - {v.constraint}: violated, contributes +{v.weight}")

    lines.append(f"certified lower bound (per level): {result.lower_bound}")
    if result.proven_optimal:
        lines.append(
            "optimality proven: search completed, so the lower bound equals "
            "the objective and no lexicographically better solution exists"
        )
    else:
        gap = tuple(
            o - l for o, l in zip(result.objective, result.lower_bound)
        )
        lines.append(
            f"optimality not proven (node limit reached); "
            f"component-wise gap to lower bound: {gap}"
        )
    return "\n".join(lines)
