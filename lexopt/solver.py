"""Depth-first branch-and-bound solver over finite domains.

No SAT/SMT library is used: the search is plain backtracking with
constraint-triggered evaluation and lexicographic bound pruning.

Correctness invariants
----------------------
- Hard constraints are evaluated as soon as their scope is fully assigned;
  a failing hard constraint prunes the branch unconditionally, so no
  penalty can ever buy through a hard conflict.
- Soft penalties are non-negative integers and accumulate monotonically
  along a branch, so a partial penalty vector that is lexicographically
  not better than the incumbent prunes the branch soundly.
- Objective vectors are compared as Python tuples, which is exactly
  lexicographic order: a higher-level unit can never be offset by any
  amount of lower-level gain.
- The reported lower bound is the lexicographic minimum over the incumbent
  and the accumulated penalty vectors of all unexplored frontier nodes;
  when the search completes, the frontier is empty and the bound is tight.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .model import Constraint, Model

OPTIMAL = "OPTIMAL"
FEASIBLE = "FEASIBLE"
INFEASIBLE = "INFEASIBLE"
UNKNOWN = "UNKNOWN"


@dataclass(frozen=True)
class Violation:
    """One violated soft constraint and its penalty contribution."""

    constraint: str
    level: int
    weight: int


@dataclass(frozen=True)
class SolveResult:
    status: str
    assignment: dict[str, Any] | None
    objective: tuple[int, ...] | None
    lower_bound: tuple[int, ...] | None
    violations: tuple[Violation, ...]
    nodes_explored: int
    proven_optimal: bool


class Solver:
    def __init__(self, model: Model, max_nodes: int | None = None) -> None:
        if max_nodes is not None and max_nodes < 0:
            raise ValueError("max_nodes must be >= 0 or None")
        self.model = model
        self.max_nodes = max_nodes

    def solve(self) -> SolveResult:
        model = self.model
        num_levels = model.num_levels
        order = sorted(model.variables, key=lambda v: (len(v.domain), v.name))
        position = {v.name: idx for idx, v in enumerate(order)}

        triggers: list[list[Constraint]] = [[] for _ in order]
        base = [0] * num_levels
        for c in model.constraints:
            if not c.scope:
                if not c.predicate({}):
                    if c.hard:
                        return SolveResult(
                            status=INFEASIBLE,
                            assignment=None,
                            objective=None,
                            lower_bound=None,
                            violations=(),
                            nodes_explored=0,
                            proven_optimal=False,
                        )
                    base[c.level] += c.weight
            else:
                triggers[max(position[n] for n in c.scope)].append(c)

        best_obj: tuple[int, ...] | None = None
        best_assign: dict[str, Any] | None = None
        frontier: list[tuple[int, ...]] = []
        nodes = 0
        limit_hit = False

        def search(idx: int, assignment: dict[str, Any], penalties: list[int]) -> None:
            nonlocal best_obj, best_assign, nodes, limit_hit
            if self.max_nodes is not None and nodes >= self.max_nodes:
                limit_hit = True
                frontier.append(tuple(penalties))
                return
            if idx == len(order):
                obj = tuple(penalties)
                if best_obj is None or obj < best_obj:
                    best_obj = obj
                    best_assign = dict(assignment)
                return
            var = order[idx]
            for value in var.domain:
                assignment[var.name] = value
                nodes += 1
                new_pen = list(penalties)
                feasible = True
                for c in triggers[idx]:
                    if not c.predicate(assignment):
                        if c.hard:
                            feasible = False
                            break
                        new_pen[c.level] += c.weight
                if feasible:
                    new_t = tuple(new_pen)
                    if best_obj is None or new_t < best_obj:
                        search(idx + 1, assignment, new_pen)
                del assignment[var.name]

        search(0, {}, base)

        if best_obj is None:
            if frontier:
                return SolveResult(UNKNOWN, None, None, min(frontier), (), nodes, False)
            return SolveResult(INFEASIBLE, None, None, None, (), nodes, False)

        lower_bound = min([best_obj, *frontier])
        violations = tuple(
            Violation(c.name, c.level, c.weight)
            for c in model.constraints
            if not c.hard and not c.predicate(best_assign)
        )
        status = OPTIMAL if not limit_hit else FEASIBLE
        return SolveResult(
            status=status,
            assignment=best_assign,
            objective=best_obj,
            lower_bound=lower_bound,
            violations=violations,
            nodes_explored=nodes,
            proven_optimal=not limit_hit,
        )
