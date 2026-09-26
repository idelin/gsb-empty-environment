"""Cheapest-insertion heuristic (no solver calls).

Repeatedly inserts each unrouted customer at its cheapest feasible
position across all open routes, opening a new route (while vehicles
remain) when that is the cheapest option.  Feasibility of every tentative
insertion is checked exactly (capacity, time windows, horizon) with
rational arithmetic.

A miss is reported as NOT_FOUND, which is explicitly *not* a claim of
infeasibility -- only the exact solver can prove INFEASIBLE.
"""

from __future__ import annotations

from fractions import Fraction

from .model import Problem, Result, Status
from .verify import route_distance_if_feasible


def solve_heuristic(problem: Problem) -> Result:
    n = len(problem.customers)
    unplaced = set(range(n))
    routes = []        # list of lists of customer indices
    route_cost = []    # exact distance of each route

    while unplaced:
        best = None  # (delta, customer, route_index == len(routes) means new route, position)
        for c in sorted(unplaced):
            if len(routes) < problem.n_vehicles:
                cost = route_distance_if_feasible(problem, [c])
                if cost is not None:
                    cand = (cost, c, len(routes), 0)
                    if best is None or cand < best:
                        best = cand
            for ri, route in enumerate(routes):
                for pos in range(len(route) + 1):
                    trial = route[:pos] + [c] + route[pos:]
                    cost = route_distance_if_feasible(problem, trial)
                    if cost is None:
                        continue
                    cand = (cost - route_cost[ri], c, ri, pos)
                    if best is None or cand < best:
                        best = cand
        if best is None:
            names = ", ".join(problem.customers[j].name for j in sorted(unplaced))
            return Result(Status.NOT_FOUND, None, (), 0,
                          f"heuristic could not place: {names} "
                          f"(this does not prove infeasibility)")
        _, c, ri, pos = best
        if ri == len(routes):
            routes.append([c])
            route_cost.append(route_distance_if_feasible(problem, [c]))
        else:
            routes[ri].insert(pos, c)
            route_cost[ri] = route_distance_if_feasible(problem, routes[ri])
        unplaced.discard(c)

    objective = sum(route_cost, start=Fraction(0))
    return Result(Status.FEASIBLE, objective, tuple(tuple(r) for r in routes), 0,
                  "insertion heuristic solution; optimality not claimed")
