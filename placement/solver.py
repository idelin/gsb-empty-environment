"""Exact branch-and-bound placement solver (no external solver).

The search enumerates, per object, the candidate replica sets (combinations
of nodes in pairwise distinct racks), then assigns objects in a
hardest-first order with capacity / anti-affinity pruning and a
cheapest-candidate lower bound.  All arithmetic is exact (Fraction).

Result statuses are kept strictly separate:

- OPTIMAL           search completed; returned placement is provably optimal
                    (and within budget when a budget was given)
- FEASIBLE          a valid placement within budget was found, but the search
                    budget (node_limit) ran out before optimality was proven
- BUDGET_UNDECIDED  no placement within budget was found and the search
                    budget ran out, so budget attainability is undecided
- BUDGET_EXCEEDED   search completed and the optimum exceeds the budget
- INFEASIBLE        no valid placement exists at all (proven structurally or
                    by exhaustive search); diagnostics explain the shortage
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction

from .diagnose import diagnose
from .model import Problem, to_fraction


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"
    BUDGET_EXCEEDED = "budget_exceeded"
    INFEASIBLE = "infeasible"


@dataclass
class SolveResult:
    status: Status
    placement: dict[str, tuple[str, ...]] | None
    cost: Fraction | None
    nodes_explored: int
    diagnostics: list[str] = field(default_factory=list)


class _SearchLimitHit(Exception):
    pass


def _check_costs_complete(problem: Problem) -> None:
    missing = [
        (obj.obj_id, node.node_id)
        for obj in problem.objects
        for node in problem.nodes
        if (obj.obj_id, node.node_id) not in problem.costs
    ]
    if missing:
        raise ValueError(f"missing transmission costs for: {missing}")


def _candidates_for(problem: Problem, obj) -> list[tuple[Fraction, tuple[str, ...]]]:
    """All replica sets for obj: distinct racks, each node individually fits."""
    cands: list[tuple[Fraction, tuple[str, ...]]] = []
    for combo in itertools.combinations(problem.nodes, obj.replicas):
        if len({n.rack for n in combo}) < obj.replicas:
            continue  # same-rack nodes never satisfy rack isolation
        if any(n.capacity < obj.size for n in combo):
            continue
        cost = sum((problem.costs[(obj.obj_id, n.node_id)] for n in combo), Fraction(0))
        cands.append((cost, tuple(n.node_id for n in combo)))
    cands.sort(key=lambda item: (item[0], item[1]))
    return cands


def solve(problem: Problem, budget=None, node_limit: int = 200_000) -> SolveResult:
    """Solve the placement problem; see module docstring for statuses."""
    _check_costs_complete(problem)
    budget = to_fraction(budget) if budget is not None else None

    candidates = {obj.obj_id: _candidates_for(problem, obj) for obj in problem.objects}
    if any(not cands for cands in candidates.values()):
        # Some object cannot get its replicas onto distinct racks at all.
        return SolveResult(Status.INFEASIBLE, None, None, 0, diagnose(problem))

    order = sorted(problem.objects, key=lambda o: (len(candidates[o.obj_id]), o.obj_id))
    suffix_lb = [Fraction(0)] * (len(order) + 1)
    for i in range(len(order) - 1, -1, -1):
        suffix_lb[i] = suffix_lb[i + 1] + candidates[order[i].obj_id][0][0]

    residual = {n.node_id: n.capacity for n in problem.nodes}
    occupants: dict[str, list[str]] = {n.node_id: [] for n in problem.nodes}
    assignment: dict[str, tuple[str, ...]] = {}

    best_assignment: dict[str, tuple[str, ...]] | None = None
    best_cost: Fraction | None = None
    explored = 0
    completed = True

    def has_conflict(obj_id: str, node_ids: tuple[str, ...]) -> bool:
        foes = problem.anti_affinity.get(obj_id, frozenset())
        return any(
            other in foes for nid in node_ids for other in occupants[nid]
        )

    def dfs(i: int, cost_so_far: Fraction) -> None:
        nonlocal best_assignment, best_cost, explored
        if best_cost is not None and cost_so_far + suffix_lb[i] >= best_cost:
            return
        if i == len(order):
            best_assignment = dict(assignment)
            best_cost = cost_so_far
            return
        obj = order[i]
        for cand_cost, cand_nodes in candidates[obj.obj_id]:
            if best_cost is not None and cost_so_far + cand_cost + suffix_lb[i + 1] >= best_cost:
                break  # candidates are cost-sorted; later ones cannot improve
            if any(residual[nid] < obj.size for nid in cand_nodes):
                continue
            if has_conflict(obj.obj_id, cand_nodes):
                continue
            if explored >= node_limit:
                raise _SearchLimitHit()
            explored += 1
            for nid in cand_nodes:
                residual[nid] -= obj.size
                occupants[nid].append(obj.obj_id)
            assignment[obj.obj_id] = cand_nodes
            dfs(i + 1, cost_so_far + cand_cost)
            del assignment[obj.obj_id]
            for nid in cand_nodes:
                residual[nid] += obj.size
                occupants[nid].pop()

    try:
        dfs(0, Fraction(0))
    except _SearchLimitHit:
        completed = False

    if best_assignment is None:
        diags = diagnose(problem)
        if completed:
            if not diags:
                diags = ["exhaustive search proved no feasible placement exists"]
            return SolveResult(Status.INFEASIBLE, None, None, explored, diags)
        if diags:
            return SolveResult(Status.INFEASIBLE, None, None, explored, diags)
        return SolveResult(Status.BUDGET_UNDECIDED, None, None, explored, [])

    placement = {oid: tuple(nids) for oid, nids in best_assignment.items()}
    if budget is not None and best_cost > budget:
        status = Status.BUDGET_EXCEEDED if completed else Status.BUDGET_UNDECIDED
    else:
        status = Status.OPTIMAL if completed else Status.FEASIBLE
    return SolveResult(status, placement, best_cost, explored, [])
