#!/usr/bin/env python3.11
"""Shelf-life transshipment network: exact min-cost multi-batch flow.

Model
-----
Goods are shipped in batches. Each batch has a quantity, an origin, a
destination, a ready (departure) time and an expiry time, all integers on a
finite time axis ``0..horizon``. Directed transport edges have an integer
duration, an integer capacity per departure-time slot, and a non-negative
integer cost per unit shipped.

Rules:
  * A batch may leave its origin at any integer time >= its departure time.
  * Warehouses (nodes) may hold a batch arbitrarily long (waiting is free).
  * A batch must *arrive* at its destination at a time strictly less than
    its expiry time; arriving exactly at the expiry time counts as expired.
    Consequently every node visit of a batch happens at time <= expiry - 1.
  * Edge capacity is consumed per (edge, departure time) slot and is shared
    by all batches; waiting edges have unlimited capacity and zero cost.
  * Batches are never merged: every unit keeps the expiry constraint of its
    own batch (multi-commodity flow, one commodity per batch).

Solver
------
The time-expanded network is a DAG (time strictly increases along every
arc). For each batch we enumerate all node-simple transport paths (waiting
is implicit between legs). Any feasible integral flow decomposes into such
paths, and because costs are non-negative an optimal solution never uses
cycles, so the path formulation is exact. We then search integer path-flow
assignments with depth-first branch and bound (admissible lower bound from
per-batch minimum path costs). When the search completes without hitting
resource limits the result is proven optimal; when a limit is hit the best
solution found is reported with status ``feasible_unproven`` (or ``unknown``
if none was found).

CLI
---
    python3.11 shelf_life_flow.py problem.json
    python3.11 shelf_life_flow.py < problem.json
    python3.11 shelf_life_flow.py --example

See README.md for the input/output format.
"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass


# --------------------------------------------------------------------------
# Model
# --------------------------------------------------------------------------

@dataclass(frozen=True)
class Edge:
    src: str
    dst: str
    duration: int
    capacity: int
    cost: int


@dataclass(frozen=True)
class Batch:
    bid: str
    origin: str
    dest: str
    quantity: int
    depart: int
    expiry: int


@dataclass(frozen=True)
class Problem:
    horizon: int
    edges: tuple
    batches: tuple


@dataclass(frozen=True)
class Path:
    """A sequence of transport legs; waiting between legs is implicit.

    legs: tuple of (edge_index, depart_time). Arrival of a leg is
    depart_time + edge.duration.
    """
    legs: tuple
    cost: int


class ProblemError(ValueError):
    pass


def load_problem(data: dict) -> Problem:
    try:
        horizon = int(data["horizon"])
        edges = tuple(
            Edge(str(e["from"]), str(e["to"]), int(e["duration"]),
                 int(e["capacity"]), int(e["cost"]))
            for e in data.get("edges", [])
        )
        batches = tuple(
            Batch(str(b["id"]), str(b["origin"]), str(b["destination"]),
                  int(b["quantity"]), int(b["depart"]), int(b["expiry"]))
            for b in data.get("batches", [])
        )
    except (KeyError, TypeError, ValueError) as exc:
        raise ProblemError(f"malformed problem: {exc}") from exc
    problem = Problem(horizon, edges, batches)
    validate_problem(problem)
    return problem


def validate_problem(problem: Problem) -> None:
    if problem.horizon < 0:
        raise ProblemError("horizon must be >= 0")
    for i, e in enumerate(problem.edges):
        if e.duration < 1:
            raise ProblemError(f"edge {i}: duration must be >= 1")
        if e.capacity < 0:
            raise ProblemError(f"edge {i}: capacity must be >= 0")
        if e.cost < 0:
            raise ProblemError(f"edge {i}: cost must be >= 0")
    for b in problem.batches:
        if b.quantity < 0:
            raise ProblemError(f"batch {b.bid}: quantity must be >= 0")
        if b.depart < 0:
            raise ProblemError(f"batch {b.bid}: depart must be >= 0")


# --------------------------------------------------------------------------
# Path enumeration (per batch, on the time-expanded DAG)
# --------------------------------------------------------------------------

def enumerate_paths(problem: Problem, batch: Batch, max_paths: int = 10000):
    """Return (paths, truncated): all node-simple paths of the batch.

    A path visits only times t with batch.depart <= t <= min(horizon,
    batch.expiry - 1); arrival exactly at expiry is excluded (expired).
    """
    if batch.origin == batch.dest:
        return [Path((), 0)], False
    latest = min(problem.horizon, batch.expiry - 1)
    out_edges = {}
    for ei, e in enumerate(problem.edges):
        out_edges.setdefault(e.src, []).append(ei)

    paths = []
    truncated = False
    legs = []
    visited = {batch.origin}

    def dfs(node, earliest, cost):
        nonlocal truncated
        if truncated:
            return
        if node == batch.dest:
            paths.append(Path(tuple(legs), cost))
            if len(paths) >= max_paths:
                truncated = True
            return
        for ei in out_edges.get(node, ()):
            e = problem.edges[ei]
            if e.dst in visited:
                continue
            # depart at td >= earliest, arrive at td + duration <= latest
            for td in range(earliest, latest - e.duration + 1):
                visited.add(e.dst)
                legs.append((ei, td))
                dfs(e.dst, td + e.duration, cost + e.cost)
                legs.pop()
                visited.discard(e.dst)

    dfs(batch.origin, batch.depart, 0)
    return paths, truncated


# --------------------------------------------------------------------------
# Exact branch-and-bound over integer path flows
# --------------------------------------------------------------------------

class _SearchTruncated(Exception):
    pass


def solve(problem: Problem, max_paths: int = 10000,
          max_search_nodes: int = 2_000_000) -> dict:
    batch_paths = []
    paths_truncated = False
    for b in problem.batches:
        paths, tr = enumerate_paths(problem, b, max_paths)
        paths_truncated = paths_truncated or tr
        paths.sort(key=lambda p: (p.cost, p.legs))
        batch_paths.append(paths)

    for b, paths in zip(problem.batches, batch_paths):
        if b.quantity > 0 and not paths:
            status = "unknown" if paths_truncated else "infeasible"
            return _build_result(problem, status, None, None, paths_truncated,
                                 False)

    n = len(problem.batches)
    # Branch on constrained batches first: fewest paths, then largest demand.
    order = sorted(range(n),
                   key=lambda i: (len(batch_paths[i]),
                                  -problem.batches[i].quantity))
    # Admissible lower bound: remaining batches each pay at least their
    # cheapest path cost per unit (capacities can only make it worse).
    suffix_lb = [0] * (n + 1)
    for k in range(n - 1, -1, -1):
        i = order[k]
        min_cost = min((p.cost for p in batch_paths[i]), default=0)
        suffix_lb[k] = suffix_lb[k + 1] + min_cost * problem.batches[i].quantity

    remaining = {}  # (edge_index, depart_time) -> remaining capacity
    assignments = [[] for _ in problem.batches]
    best = {"cost": None, "assign": None}
    nodes = [0]
    search_truncated = [False]

    def arc_remaining(key):
        r = remaining.get(key)
        if r is None:
            r = problem.edges[key[0]].capacity
        return r

    def dfs_batch(k, cost):
        if best["cost"] is not None and cost + suffix_lb[k] >= best["cost"]:
            return
        nodes[0] += 1
        if nodes[0] > max_search_nodes:
            search_truncated[0] = True
            raise _SearchTruncated
        if k == n:
            best["cost"] = cost
            best["assign"] = [list(a) for a in assignments]
            return
        i = order[k]
        b = problem.batches[i]
        paths = batch_paths[i]
        assigns = assignments[i]

        def dfs_path(j, remaining_q, path_cost):
            if best["cost"] is not None:
                # paths are sorted by cost, so paths[j].cost bounds every
                # unit still to place in this batch.
                unit_lb = paths[j].cost if j < len(paths) else 0
                bound = cost + path_cost + remaining_q * unit_lb \
                    + suffix_lb[k + 1]
                if bound >= best["cost"]:
                    return
            if j == len(paths):
                if remaining_q == 0:
                    dfs_batch(k + 1, cost + path_cost)
                return
            p = paths[j]
            cap = remaining_q
            for key in p.legs:
                r = arc_remaining(key)
                if r < cap:
                    cap = r
            for x in range(cap, -1, -1):
                if x:
                    for key in p.legs:
                        remaining[key] = arc_remaining(key) - x
                    assigns.append((p, x))
                dfs_path(j + 1, remaining_q - x, path_cost + x * p.cost)
                if x:
                    assigns.pop()
                    for key in p.legs:
                        restored = remaining[key] + x
                        if restored == problem.edges[key[0]].capacity:
                            del remaining[key]
                        else:
                            remaining[key] = restored

        dfs_path(0, b.quantity, 0)

    try:
        dfs_batch(0, 0)
    except _SearchTruncated:
        pass

    proven = not paths_truncated and not search_truncated[0]
    if best["cost"] is None:
        status = "infeasible" if proven else "unknown"
    else:
        status = "optimal" if proven else "feasible_unproven"
    return _build_result(problem, status, best["cost"], best["assign"],
                         paths_truncated, search_truncated[0])


# --------------------------------------------------------------------------
# Verification and output
# --------------------------------------------------------------------------

def verify(problem: Problem, assignments) -> dict:
    """Check per-batch path validity (conservation), expiry, capacity, cost."""
    checks = {"quantities_satisfied": True, "paths_valid": True,
              "expiry_respected": True, "capacities_respected": True}
    usage = {}
    total_cost = 0
    for b, assigns in zip(problem.batches, assignments):
        shipped = 0
        for path, amount in assigns:
            if amount <= 0:
                checks["paths_valid"] = False
                continue
            shipped += amount
            total_cost += amount * path.cost
            node = b.origin
            earliest = b.depart
            recomputed = 0
            for ei, td in path.legs:
                e = problem.edges[ei]
                if e.src != node or td < earliest or td > problem.horizon - e.duration:
                    checks["paths_valid"] = False
                recomputed += e.cost
                usage[(ei, td)] = usage.get((ei, td), 0) + amount
                node = e.dst
                earliest = td + e.duration
            if recomputed != path.cost:
                checks["paths_valid"] = False
            if node != b.dest:
                checks["paths_valid"] = False
            if earliest > b.expiry - 1:  # arrival at expiry counts as expired
                checks["expiry_respected"] = False
        if shipped != b.quantity:
            checks["quantities_satisfied"] = False
    for (ei, td), used in usage.items():
        if used > problem.edges[ei].capacity:
            checks["capacities_respected"] = False
    checks["all"] = all(checks.values())
    return {"checks": checks, "total_cost": total_cost, "usage": usage}


def _build_result(problem, status, total_cost, assignments,
                  paths_truncated, search_truncated):
    result = {"status": status, "optimal": status == "optimal",
              "total_cost": total_cost, "batches": [], "edge_usage": [],
              "checks": None}
    notes = []
    if paths_truncated:
        notes.append("path enumeration hit --max-paths; optimality/infeasibility not proven")
    if search_truncated:
        notes.append("search hit --max-search-nodes; optimality/infeasibility not proven")
    if assignments is None:
        result["notes"] = notes
        return result

    usage = {}
    for b, assigns in zip(problem.batches, assignments):
        entry = {"id": b.bid, "quantity": b.quantity, "paths": []}
        for path, amount in assigns:
            if amount == 0:
                continue
            legs = []
            for ei, td in path.legs:
                e = problem.edges[ei]
                legs.append({"edge": ei, "from": e.src, "to": e.dst,
                             "depart": td, "arrive": td + e.duration,
                             "cost": e.cost})
                key = (ei, td)
                usage[key] = usage.get(key, 0) + amount
            entry["paths"].append({"amount": amount, "unit_cost": path.cost,
                                   "legs": legs})
        result["batches"].append(entry)

    for (ei, td), used in sorted(usage.items()):
        e = problem.edges[ei]
        result["edge_usage"].append(
            {"edge": ei, "from": e.src, "to": e.dst, "depart": td,
             "arrive": td + e.duration, "used": used, "capacity": e.capacity})

    verification = verify(problem, assignments)
    result["checks"] = verification["checks"]
    if verification["total_cost"] != total_cost:
        result["checks"]["all"] = False
        notes.append("internal cost mismatch")
    result["notes"] = notes
    return result


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------

EXAMPLE = {
    "horizon": 5,
    "edges": [
        {"from": "A", "to": "B", "duration": 1, "capacity": 1, "cost": 2},
        {"from": "B", "to": "C", "duration": 1, "capacity": 1, "cost": 3},
        {"from": "A", "to": "C", "duration": 3, "capacity": 4, "cost": 1},
    ],
    "batches": [
        {"id": "b1", "origin": "A", "destination": "C", "quantity": 1,
         "depart": 0, "expiry": 3}
    ],
}


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Exact min-cost shelf-life transshipment solver "
                    "(time-expanded multi-commodity flow).")
    parser.add_argument("input", nargs="?",
                        help="JSON problem file (default: read stdin)")
    parser.add_argument("--max-paths", type=int, default=10000,
                        help="cap on enumerated paths per batch")
    parser.add_argument("--max-search-nodes", type=int, default=2_000_000,
                        help="cap on branch-and-bound search nodes")
    parser.add_argument("--example", action="store_true",
                        help="print an example problem JSON and exit")
    args = parser.parse_args(argv)

    if args.example:
        json.dump(EXAMPLE, sys.stdout, indent=2)
        sys.stdout.write("\n")
        return 0

    try:
        if args.input:
            with open(args.input, "r", encoding="utf-8") as fh:
                data = json.load(fh)
        else:
            data = json.load(sys.stdin)
        problem = load_problem(data)
    except (OSError, json.JSONDecodeError, ProblemError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    result = solve(problem, max_paths=args.max_paths,
                   max_search_nodes=args.max_search_nodes)
    json.dump(result, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
