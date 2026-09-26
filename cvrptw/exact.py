"""Exact branch-and-bound solver (no external solver calls).

Performs a depth-first enumeration of route sets: vehicles are identical
and routes are built sequentially, so each partition/order combination is
visited once.  Capacity, time-window and horizon feasibility prune every
extension; a nonneg-distance cost bound prunes dominated branches.

Outcome mapping:
  * search completes, incumbent found  -> OPTIMAL (objective proven minimal)
  * search completes, no incumbent     -> INFEASIBLE (proven)
  * node budget exceeded               -> BUDGET_EXHAUSTED (undecided; any
    incumbent is attached as a FEASIBLE-quality witness, not a proof)
"""

from __future__ import annotations

from fractions import Fraction

from .model import Problem, Result, Status

INF = float("inf")


class _BudgetExhausted(Exception):
    pass


def _shortest_paths(dist):
    """Floyd-Warshall over exact rationals; used only for sound pruning and
    for the provable-infeasibility pre-check (earliest possible arrival)."""
    n = len(dist)
    sp = [list(row) for row in dist]
    for k in range(n):
        spk = sp[k]
        for i in range(n):
            dik = sp[i][k]
            if dik == INF:
                continue
            spi = sp[i]
            via = dik
            for j in range(n):
                alt = via + spk[j]
                if alt < spi[j]:
                    spi[j] = alt
    return sp


def _prove_infeasible(problem: Problem):
    """Cheap, sound infeasibility certificates.  Returns a message or None."""
    total_demand = Fraction(0)
    for c in problem.customers:
        if c.demand > problem.capacity:
            return f"customer {c.name} demand {c.demand} exceeds vehicle capacity {problem.capacity}"
        total_demand += c.demand
    if total_demand > problem.capacity * problem.n_vehicles:
        return (f"total demand {total_demand} exceeds fleet capacity "
                f"{problem.capacity * problem.n_vehicles}")
    sp = _shortest_paths(problem.distances)
    for j, c in enumerate(problem.customers):
        earliest = max(c.ready, sp[0][j + 1])
        if earliest > c.due:
            return (f"customer {c.name} unreachable in time: earliest service "
                    f"{earliest} > due {c.due}")
    return None


def solve_exact(problem: Problem, node_budget: int = 200_000) -> Result:
    n = len(problem.customers)
    dist = problem.distances
    cap = problem.capacity
    horizon = problem.horizon
    customers = problem.customers

    certificate = _prove_infeasible(problem)
    if certificate is not None:
        return Result(Status.INFEASIBLE, None, (), 0, certificate)

    sp = _shortest_paths(dist)

    best_obj = [INF]
    best_routes = [None]
    nodes = [0]

    def dfs(node, depart, load, cost, unvisited, done, current):
        nodes[0] += 1
        if nodes[0] > node_budget:
            raise _BudgetExhausted
        if cost >= best_obj[0]:
            return
        if not unvisited:
            back = dist[node][0]
            if depart + back <= horizon:
                total = cost + back
                if total < best_obj[0]:
                    best_obj[0] = total
                    best_routes[0] = done + ((current,) if current else ())
            return
        # Try extending the current route, nearest candidates first.
        for j in sorted(unvisited, key=lambda c: dist[node][c + 1]):
            cust = customers[j]
            nj = j + 1
            if load + cust.demand > cap:
                continue
            arrive = depart + dist[node][nj]
            start = max(arrive, cust.ready)
            if start > cust.due:
                continue
            leave = start + cust.service
            if leave + sp[nj][0] > horizon:  # can never get home in time
                continue
            dfs(nj, leave, load + cust.demand, cost + dist[node][nj],
                unvisited - {j}, done, current + (j,))
        # Or close the current route and dispatch another vehicle.
        if current and len(done) + 1 < problem.n_vehicles:
            back = dist[node][0]
            if depart + back <= horizon:
                dfs(0, Fraction(0), Fraction(0), cost + back,
                    unvisited, done + (current,), ())

    try:
        dfs(0, Fraction(0), Fraction(0), Fraction(0), frozenset(range(n)), (), ())
    except _BudgetExhausted:
        if best_routes[0] is not None:
            return Result(Status.BUDGET_EXHAUSTED, best_obj[0], best_routes[0],
                          nodes[0], "node budget exhausted; incumbent is feasible "
                                    "but optimality is undecided")
        return Result(Status.BUDGET_EXHAUSTED, None, (), nodes[0],
                      "node budget exhausted before any solution; feasibility undecided")

    if best_routes[0] is None:
        return Result(Status.INFEASIBLE, None, (), nodes[0],
                      "exhaustive search found no feasible route set")
    return Result(Status.OPTIMAL, best_obj[0], best_routes[0], nodes[0],
                  "proven optimal by exhaustive search")
