"""Exact small-scale solver: branch and bound with a proven lower bound.

Objective is lexicographic: first minimize the maximum slot load, then
minimize total migrated bytes. All arithmetic is exact integer math.
If the search is cut off by the node limit, the result is UNDECIDED and
carries the proven lower bound plus the remaining gap.
"""
from __future__ import annotations

from dataclasses import dataclass

from .model import Problem

OPTIMAL = "OPTIMAL"
UNDECIDED = "UNDECIDED"
INFEASIBLE = "INFEASIBLE"


@dataclass(frozen=True)
class SolveResult:
    status: str
    assignment: dict | None  # pid -> sid, independently verifiable
    max_load: int | None
    moved_bytes: int | None
    lower_bound: int  # proven lower bound on the optimal max load
    gap: int | None  # max_load - lower_bound when undecided, 0 when optimal
    unmet_bytes: int  # capacity that cannot be satisfied, when infeasible
    detail: str
    nodes: int


@dataclass(frozen=True)
class Move:
    pid: str
    src: str
    dst: str
    weight: int


@dataclass(frozen=True)
class Plan:
    plan_id: str
    generation: int
    moves: tuple  # tuple[Move, ...], deterministically ordered

    @property
    def moved_bytes(self) -> int:
        return sum(m.weight for m in self.moves)


def build_plan(problem: Problem, result: SolveResult, plan_id: str,
               generation: int) -> Plan:
    if result.assignment is None:
        raise ValueError("no assignment to plan from")
    weights = problem.weights
    moves = [
        Move(pid, problem.initial[pid], result.assignment[pid], weights[pid])
        for pid in problem.initial
        if problem.initial[pid] != result.assignment[pid]
    ]
    moves.sort(key=lambda m: (m.pid, m.src, m.dst))
    return Plan(plan_id=plan_id, generation=generation, moves=tuple(moves))


def verify(problem: Problem, assignment: dict) -> list:
    """Independent checker: returns a list of violations (empty == valid)."""
    errors = []
    weights = problem.weights
    if set(assignment) != set(weights):
        errors.append("assignment does not cover every partition exactly")
        return errors
    sids = set(problem.capacities)
    loads = {sid: 0 for sid in sids}
    for pid, sid in assignment.items():
        if sid not in sids:
            errors.append(f"partition {pid} assigned to unknown slot {sid}")
            continue
        loads[sid] += weights[pid]
    for sid, load in loads.items():
        if load > problem.capacities[sid]:
            errors.append(f"slot {sid} overflow: {load} > {problem.capacities[sid]}")
    for pair in problem.affinity:
        a, b = sorted(pair)
        if assignment[a] != assignment[b]:
            errors.append(f"affinity violated: {a} and {b} separated")
    for pair in problem.anti_affinity:
        a, b = sorted(pair)
        if assignment[a] == assignment[b]:
            errors.append(f"anti-affinity violated: {a} and {b} co-located")
    moved = sum(weights[pid] for pid in weights
                if assignment[pid] != problem.initial[pid])
    if moved > problem.budget:
        errors.append(f"budget exceeded: {moved} > {problem.budget}")
    return errors


def _min_max_load_lb(weights: list, residual_caps: list) -> int:
    """Smallest T with sum(min(cap, T)) >= sum(weights); exact bisection."""
    total = sum(weights)
    if total == 0:
        return 0
    lo, hi = 0, max(max(residual_caps, default=0), max(weights, default=0))
    while lo < hi:
        mid = (lo + hi) // 2
        if sum(min(c, mid) for c in residual_caps) >= total:
            hi = mid
        else:
            lo = mid + 1
    return lo


class _Abort(Exception):
    pass


def solve(problem: Problem, node_limit: int = 200_000) -> SolveResult:
    slots = sorted(problem.slots, key=lambda s: s.sid)
    parts = sorted(problem.partitions, key=lambda p: (-p.weight, p.pid))
    n = len(parts)
    caps = [s.capacity for s in slots]
    sid_of = [s.sid for s in slots]
    index = {s.sid: i for i, s in enumerate(slots)}
    initial_idx = {pid: index[sid] for pid, sid in problem.initial.items()}

    total_weight = sum(p.weight for p in parts)
    max_cap = max(caps) if caps else 0
    oversize = sum(p.weight - max_cap for p in parts if p.weight > max_cap)
    shortfall = total_weight - sum(caps)
    if oversize > 0 or shortfall > 0:
        unmet = max(oversize, shortfall)
        detail = ("a partition exceeds every slot and cannot be split"
                  if oversize >= shortfall else "total capacity insufficient")
        return SolveResult(INFEASIBLE, None, None, None, 0, None, unmet, detail, 0)

    aff = {p.pid: set() for p in parts}
    anti = {p.pid: set() for p in parts}
    for pair in problem.affinity:
        a, b = sorted(pair)
        aff[a].add(b)
        aff[b].add(a)
    for pair in problem.anti_affinity:
        a, b = sorted(pair)
        anti[a].add(b)
        anti[b].add(a)

    pids = [p.pid for p in parts]
    weights = [p.weight for p in parts]
    pos = {pid: k for k, pid in enumerate(pids)}
    loads = [0] * len(slots)
    assign = [-1] * n
    members = [set() for _ in slots]
    best = {"obj": None, "assign": None}
    nodes = 0

    def lower_bound(k: int) -> int:
        residual = [caps[j] - loads[j] for j in range(len(slots))]
        remaining = weights[k:]
        return max(max(loads, default=0), _min_max_load_lb(remaining, residual))

    root_lb = lower_bound(0)

    def recurse(k: int, moved: int) -> None:
        nonlocal nodes
        if nodes >= node_limit:
            raise _Abort()
        nodes += 1
        if moved > problem.budget:
            return
        if best["obj"] is not None and (lower_bound(k), moved) >= best["obj"]:
            return
        if k == n:
            obj = (max(loads, default=0), moved)
            if best["obj"] is None or obj < best["obj"]:
                best["obj"] = obj
                best["assign"] = assign.copy()
            return
        pid = pids[k]
        w = weights[k]
        forced = None
        for other in aff[pid]:
            j = assign[pos[other]]
            if j >= 0:
                forced = j
                break
        candidates = (forced,) if forced is not None else range(len(slots))
        for j in candidates:
            if loads[j] + w > caps[j]:
                continue
            if members[j] & anti[pid]:
                continue
            assign[k] = j
            loads[j] += w
            members[j].add(pid)
            recurse(k + 1, moved + (0 if j == initial_idx[pid] else w))
            members[j].discard(pid)
            loads[j] -= w
            assign[k] = -1

    aborted = False
    try:
        recurse(0, 0)
    except _Abort:
        aborted = True

    if best["obj"] is None:
        if aborted:
            return SolveResult(UNDECIDED, None, None, None, root_lb, None, 0,
                               "search budget exhausted before any solution", nodes)
        return SolveResult(INFEASIBLE, None, None, None, root_lb, None, 0,
                           "placement constraints unsatisfiable", nodes)

    assignment = {pids[i]: sid_of[best["assign"][i]] for i in range(n)}
    max_load, moved = best["obj"]
    if aborted:
        return SolveResult(UNDECIDED, assignment, max_load, moved, root_lb,
                           max_load - root_lb, 0,
                           "incumbent not proven optimal within node limit", nodes)
    return SolveResult(OPTIMAL, assignment, max_load, moved, root_lb, 0, 0,
                       "proven optimal", nodes)
