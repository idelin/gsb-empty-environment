"""Asymmetric CVRPTW solver for synthetic instances (no external solver, no map data).

- Distances may be asymmetric: d(a, b) is never assumed equal to d(b, a).
- Every demand node is visited exactly once.
- All constraint data are exact numbers (int or fractions.Fraction).
- Statuses are kept distinct:
    OPTIMAL             exact search exhausted, returned objective is proven optimal
    FEASIBLE            a solution was found but optimality was not proven
                        (budget exhausted with an incumbent, or heuristic result)
    INFEASIBLE          proven that no feasible solution exists
    BUDGET_UNDECIDED    search budget exhausted: no solution and no infeasibility proof
    HEURISTIC_NOT_FOUND heuristic failed to find a solution; feasibility unknown
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from typing import Optional


def _num(x) -> Fraction:
    if isinstance(x, Fraction):
        return x
    if isinstance(x, bool):
        raise TypeError("bool is not valid constraint data")
    if isinstance(x, int):
        return Fraction(x)
    if isinstance(x, float):
        return Fraction(str(x))
    raise TypeError(f"unsupported number: {x!r}")


def fmt(x: Fraction) -> str:
    return str(x.numerator) if x.denominator == 1 else f"{x.numerator}/{x.denominator}"


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    INFEASIBLE = "infeasible"
    BUDGET_UNDECIDED = "budget_undecided"
    HEURISTIC_NOT_FOUND = "heuristic_not_found"


@dataclass
class Instance:
    """Node 0 is the depot; nodes 1..n are customers. Travel time == distance."""

    n: int
    dist: list  # (n+1) x (n+1) of Fraction, asymmetric allowed
    demand: list  # Fraction, demand[0] == 0
    capacity: Fraction
    early: list  # earliest service start, per node
    late: list  # latest arrival, per node (depot: latest return)
    service: list  # service duration per node
    max_vehicles: Optional[int] = None

    @classmethod
    def from_lists(cls, dist, demand, capacity, early, late, service, max_vehicles=None):
        n = len(dist) - 1
        if n < 0 or any(len(row) != n + 1 for row in dist):
            raise ValueError("dist must be a square (n+1) x (n+1) matrix")
        for name, vec in (("demand", demand), ("early", early), ("late", late), ("service", service)):
            if len(vec) != n + 1:
                raise ValueError(f"{name} must have length n+1")
        d = [[_num(v) for v in row] for row in dist]
        dem = [_num(v) for v in demand]
        cap = _num(capacity)
        ea = [_num(v) for v in early]
        la = [_num(v) for v in late]
        sv = [_num(v) for v in service]
        if dem[0] != 0:
            raise ValueError("depot demand must be 0")
        if cap <= 0:
            raise ValueError("capacity must be positive")
        for i in range(n + 1):
            if any(v < 0 for v in d[i]):
                raise ValueError("distances must be non-negative")
            if dem[i] < 0:
                raise ValueError("demands must be non-negative")
            if ea[i] > la[i]:
                raise ValueError(f"empty time window at node {i}")
            if sv[i] < 0:
                raise ValueError("service times must be non-negative")
        if max_vehicles is not None and max_vehicles < 1:
            raise ValueError("max_vehicles must be >= 1")
        return cls(n, d, dem, cap, ea, la, sv, max_vehicles)


@dataclass
class SolveResult:
    status: Status
    objective: Optional[Fraction]  # actual total distance of the returned routes
    routes: Optional[list]  # list of routes; each route is a list of customer ids
    nodes: int  # branch-and-bound nodes explored (0 for heuristic)
    method: str
    detail: str
    report: list = field(default_factory=list)  # per-segment verification lines


def verify_routes(inst: Instance, routes):
    """Re-simulate every leg. Returns (ok, lines, total_distance).

    Each leg reports departure, travel, arrival, waiting, service span and the
    cumulative load against capacity; time-window and capacity violations are
    flagged inline. Also checks that every customer is visited exactly once.
    """
    d, dem, cap = inst.dist, inst.demand, inst.capacity
    ea, la, sv = inst.early, inst.late, inst.service
    lines = []
    ok = True
    seen = set()
    total = Fraction(0)
    for r, route in enumerate(routes, 1):
        if not route:
            lines.append(f"route {r}: empty route -> VIOLATION")
            ok = False
            continue
        lines.append(f"route {r}: 0 -> " + " -> ".join(map(str, route)) + " -> 0")
        load = Fraction(0)
        time = Fraction(0)
        prev = 0
        for j in route:
            if not (1 <= j <= inst.n):
                lines.append(f"  ->{j}: unknown customer -> VIOLATION")
                ok = False
                continue
            leg = d[prev][j]
            arr = time + leg
            wait = ea[j] - arr if arr < ea[j] else Fraction(0)
            start = arr + wait
            load += dem[j]
            problems = []
            if j in seen:
                problems.append("duplicate visit")
            seen.add(j)
            if arr > la[j]:
                problems.append(f"window miss (arrive {fmt(arr)} > latest {fmt(la[j])})")
            if load > cap:
                problems.append(f"overload ({fmt(load)} > capacity {fmt(cap)})")
            total += leg
            state = "ok" if not problems else "VIOLATION: " + "; ".join(problems)
            lines.append(
                f"  {prev}->{j}: depart {fmt(time)}, travel {fmt(leg)}, arrive {fmt(arr)}, "
                f"wait {fmt(wait)}, serve [{fmt(start)}, {fmt(start + sv[j])}], "
                f"load {fmt(load)}/{fmt(cap)}  {state}"
            )
            if problems:
                ok = False
            time = start + sv[j]
            prev = j
        leg = d[prev][0]
        arr = time + leg
        total += leg
        problems = []
        if arr > la[0]:
            problems.append(f"depot window miss (return {fmt(arr)} > latest {fmt(la[0])})")
        state = "ok" if not problems else "VIOLATION: " + "; ".join(problems)
        lines.append(f"  {prev}->0: depart {fmt(time)}, travel {fmt(leg)}, arrive {fmt(arr)}  {state}")
        if problems:
            ok = False
    missing = sorted(set(range(1, inst.n + 1)) - seen)
    if missing:
        lines.append(f"missing customers: {missing} -> VIOLATION")
        ok = False
    lines.append(f"total distance = {fmt(total)}")
    return ok, lines, total


def solve_exact(inst: Instance, node_limit: Optional[int] = 1_000_000) -> SolveResult:
    """Depth-first branch and bound over route sequences.

    Symmetry between identical vehicles is broken by requiring the first
    customer of each route to increase along the route sequence.
    """
    n = inst.n
    if n == 0:
        return SolveResult(Status.OPTIMAL, Fraction(0), [], 0, "exact", "no customers", [])
    d, dem, cap = inst.dist, inst.demand, inst.capacity
    ea, la, sv = inst.early, inst.late, inst.service

    for j in range(1, n + 1):
        if dem[j] > cap:
            return SolveResult(
                Status.INFEASIBLE, None, None, 0, "exact",
                f"demand[{j}]={fmt(dem[j])} exceeds capacity {fmt(cap)}",
            )
    min_in = [Fraction(0)] * (n + 1)
    for j in range(1, n + 1):
        min_in[j] = min(d[i][j] for i in range(n + 1) if i != j)
        if min_in[j] > la[j]:
            return SolveResult(
                Status.INFEASIBLE, None, None, 0, "exact",
                f"customer {j} unreachable within its window "
                f"(earliest possible arrival {fmt(min_in[j])} > latest {fmt(la[j])})",
            )

    full = (1 << n) - 1
    best_obj = None
    best_routes = None
    nodes = 0
    exhausted = False

    def dfs(routes, current, time, load, visited, cost):
        nonlocal best_obj, best_routes, nodes, exhausted
        if exhausted:
            return
        if node_limit is not None and nodes >= node_limit:
            exhausted = True
            return
        nodes += 1
        if best_obj is not None:
            lb = cost
            for j in range(1, n + 1):
                if not (visited >> (j - 1)) & 1:
                    lb += min_in[j]
            if lb >= best_obj:
                return
        last = current[-1]
        if visited == full:
            if time + d[last][0] <= la[0]:
                total = cost + d[last][0]
                if best_obj is None or total < best_obj:
                    best_obj = total
                    best_routes = routes + [current]
            return
        # 1) extend the current route
        for j in range(1, n + 1):
            bit = 1 << (j - 1)
            if visited & bit or load + dem[j] > cap:
                continue
            arr = time + d[last][j]
            if arr > la[j]:
                continue
            start = ea[j] if arr < ea[j] else arr
            dfs(routes, current + [j], start + sv[j], load + dem[j],
                visited | bit, cost + d[last][j])
            if exhausted:
                return
        # 2) close the current route at the depot and open a new one
        if time + d[last][0] <= la[0]:
            if inst.max_vehicles is None or len(routes) + 2 <= inst.max_vehicles:
                back = d[last][0]
                first_prev = current[0]
                for j in range(1, n + 1):
                    bit = 1 << (j - 1)
                    if visited & bit or j <= first_prev or dem[j] > cap:
                        continue
                    arr = d[0][j]
                    if arr > la[j]:
                        continue
                    start = ea[j] if arr < ea[j] else arr
                    dfs(routes + [current], [j], start + sv[j], dem[j],
                        visited | bit, cost + back + d[0][j])
                    if exhausted:
                        return

    for j in range(1, n + 1):
        if dem[j] > cap:
            continue
        arr = d[0][j]
        if arr > la[j]:
            continue
        start = ea[j] if arr < ea[j] else arr
        dfs([], [j], start + sv[j], dem[j], 1 << (j - 1), d[0][j])
        if exhausted:
            break

    if best_obj is None:
        if exhausted:
            return SolveResult(Status.BUDGET_UNDECIDED, None, None, nodes, "exact",
                               f"node budget {node_limit} exhausted before any solution or proof")
        return SolveResult(Status.INFEASIBLE, None, None, nodes, "exact",
                           "search exhausted: no feasible route set exists")
    status = Status.FEASIBLE if exhausted else Status.OPTIMAL
    detail = ("incumbent returned; optimality not proven (node budget exhausted)"
              if exhausted else "search exhausted: objective is proven optimal")
    ok, report, total = verify_routes(inst, best_routes)
    assert ok and total == best_obj, "internal verification mismatch"
    return SolveResult(status, best_obj, best_routes, nodes, "exact", detail, report)


def _greedy(inst: Instance, mode: str):
    d, dem, cap = inst.dist, inst.demand, inst.capacity
    ea, la, sv = inst.early, inst.late, inst.service
    unvisited = set(range(1, inst.n + 1))
    routes = []
    while unvisited:
        route = []
        time = Fraction(0)
        load = Fraction(0)
        prev = 0
        while True:
            cands = [j for j in unvisited
                     if load + dem[j] <= cap and time + d[prev][j] <= la[j]]
            if not cands:
                break
            if mode == "nearest":
                j = min(cands, key=lambda c: (d[prev][c], c))
            else:  # deadline: most urgent window first
                j = min(cands, key=lambda c: (la[c], d[prev][c], c))
            arr = time + d[prev][j]
            start = ea[j] if arr < ea[j] else arr
            time = start + sv[j]
            load += dem[j]
            route.append(j)
            unvisited.discard(j)
            prev = j
        if time + d[prev][0] > la[0]:
            return None
        if not route:
            return None  # remaining customers unreachable from the depot
        routes.append(route)
        if inst.max_vehicles is not None and len(routes) >= inst.max_vehicles and unvisited:
            return None
    return routes


def solve_heuristic(inst: Instance) -> SolveResult:
    """Deterministic greedy insertion (nearest-neighbour and deadline-first)."""
    best = None
    for mode in ("nearest", "deadline"):
        routes = _greedy(inst, mode)
        if routes is None:
            continue
        ok, _, total = verify_routes(inst, routes)
        if not ok:
            continue
        if best is None or total < best[0]:
            best = (total, routes)
    if best is None:
        return SolveResult(Status.HEURISTIC_NOT_FOUND, None, None, 0, "heuristic",
                           "greedy construction found no feasible solution; "
                           "feasibility is NOT decided (use solve_exact for a proof)")
    ok, report, _ = verify_routes(inst, best[1])
    return SolveResult(Status.FEASIBLE, best[0], best[1], 0, "heuristic",
                       "feasible solution from heuristic; optimality not proven", report)
