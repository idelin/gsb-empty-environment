"""Bounded-memory recomputation planner for DAG compute graphs.

Given a directed acyclic compute graph where each node has an integer
compute cost and an integer result size, and a fixed memory limit, find a
minimum-total-compute-cost sequence of node evaluations that leaves all
requested outputs resident in memory.

Rules of the model:
  * Computing a node requires ALL of its parents' results to be resident.
  * While computing node n, the transient peak memory is
    size(n) + sum(size(p) for p in parents(n)); this must fit the limit,
    otherwise the node can never be computed (the plan is infeasible).
    Parents are never implicitly dropped during a computation.
  * Eviction of cached results is free and may happen after any step.
  * A node may be recomputed; each computation pays its cost again.

The search is an exact Dijkstra exploration over cache states (the set of
resident results), restricted to "maximal kept sets" transitions to keep the
state space small, with superset-dominance pruning.  An optional
expansion budget interrupts the search while preserving a valid lower
bound (Dijkstra frontier minimum) and, if any goal state was generated,
an upper bound on the optimum.

Python 3.11, standard library only.
"""

from __future__ import annotations

import argparse
import heapq
import itertools
import json
import sys

INF = float("inf")


class PlanError(Exception):
    """Raised for malformed input graphs (cycles, bad refs, bad values)."""


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

def validate_nodes(node_list):
    """Build {id: {"cost","size","parents"}} from a list of node dicts."""
    nodes = {}
    for entry in node_list:
        try:
            nid = entry["id"]
            cost = entry["cost"]
            size = entry["size"]
            parents = entry.get("parents", [])
        except (KeyError, TypeError) as exc:
            raise PlanError(f"malformed node entry: {entry!r}") from exc
        if not isinstance(nid, str) or not nid:
            raise PlanError(f"node id must be a non-empty string: {nid!r}")
        if nid in nodes:
            raise PlanError(f"duplicate node id: {nid!r}")
        if not isinstance(cost, int) or isinstance(cost, bool) or cost < 0:
            raise PlanError(f"node {nid!r}: cost must be a non-negative integer")
        if not isinstance(size, int) or isinstance(size, bool) or size < 0:
            raise PlanError(f"node {nid!r}: size must be a non-negative integer")
        if not isinstance(parents, list) or len(set(parents)) != len(parents):
            raise PlanError(f"node {nid!r}: parents must be a list of unique ids")
        nodes[nid] = {"cost": cost, "size": size, "parents": list(parents)}
    for nid, spec in nodes.items():
        for parent in spec["parents"]:
            if parent not in nodes:
                raise PlanError(f"node {nid!r}: unknown parent reference {parent!r}")
    _check_acyclic(nodes)
    return nodes


def _check_acyclic(nodes):
    indegree = {nid: 0 for nid in nodes}
    children = {nid: [] for nid in nodes}
    for nid, spec in nodes.items():
        for parent in spec["parents"]:
            indegree[nid] += 1
            children[parent].append(nid)
    queue = [nid for nid, deg in indegree.items() if deg == 0]
    seen = 0
    while queue:
        nid = queue.pop()
        seen += 1
        for child in children[nid]:
            indegree[child] -= 1
            if indegree[child] == 0:
                queue.append(child)
    if seen != len(nodes):
        raise PlanError("graph contains a cycle")


def _ancestors_needed(nodes, outputs):
    needed = set()
    stack = list(outputs)
    while stack:
        nid = stack.pop()
        if nid in needed:
            continue
        needed.add(nid)
        stack.extend(nodes[nid]["parents"])
    return needed


def peak_memory(nodes, nid):
    """Transient memory required while computing nid (result + parents)."""
    spec = nodes[nid]
    return spec["size"] + sum(nodes[p]["size"] for p in spec["parents"])


# ---------------------------------------------------------------------------
# Kept-set enumeration
# ---------------------------------------------------------------------------

def _maximal_kept_sets(base, must_keep, sizes, limit):
    """All maximal subsets of `base` containing `must_keep` that fit `limit`.

    Eviction is free, so after computing `must_keep` any fitting subset of
    the cache may be retained; a non-maximal subset is dominated by a
    maximal superset reachable at the same cost, hence only maximal sets
    need to be considered.
    """
    others = sorted(base - {must_keep})
    fitting = []

    def rec(idx, kept, used):
        if idx == len(others):
            fitting.append(frozenset(kept))
            return
        node = others[idx]
        rec(idx + 1, kept, used)  # exclude
        if used + sizes[node] <= limit:  # include
            kept.append(node)
            rec(idx + 1, kept, used + sizes[node])
            kept.pop()

    rec(0, [must_keep], sizes[must_keep])
    maximal = []
    for kept in fitting:
        room = limit - sum(sizes[x] for x in kept)
        if all(sizes[x] > room for x in base - kept):
            maximal.append(kept)
    return maximal


# ---------------------------------------------------------------------------
# Exact search
# ---------------------------------------------------------------------------

def solve(nodes, outputs, memory_limit, expansion_budget=None):
    """Exact minimum-cost plan via Dijkstra over cache states.

    Returns a result dict with keys:
      status         "optimal" | "infeasible" | "interrupted"
      total_cost       optimal/upper-bound cost when a plan exists
      steps          per-step plan with memory usage and dependency evidence
      lower_bound    certified lower bound on the optimum (if computable)
      upper_bound    best known feasible cost (if any goal was generated)
      expansions     number of states expanded
    """
    if not isinstance(memory_limit, int) or memory_limit < 0:
        raise PlanError("memory_limit must be a non-negative integer")
    outputs = frozenset(outputs)
    unknown = outputs - nodes.keys()
    if unknown:
        raise PlanError(f"unknown output output(s): {sorted(unknown)}")

    sizes = {nid: spec["size"] for nid, spec in nodes.items()}
    needed = _ancestors_needed(nodes, outputs)

    violations = [
        {"node": nid, "peak_memory": peak_memory(nodes, nid)}
        for nid in sorted(needed)
        if peak_memory(nodes, nid) > memory_limit
    ]
    if violations:
        return {
            "status": "infeasible",
            "reason": "node plus resident parents exceed the memory limit; "
                      "parents cannot be dropped implicitly",
            "violations": violations,
            "memory_limit": memory_limit,
        }
    outputs_size = sum(sizes[o] for o in outputs)
    if outputs_size > memory_limit:
        return {
            "status": "infeasible",
            "reason": "requested outputs cannot be resident simultaneously",
            "outputs_size": outputs_size,
            "memory_limit": memory_limit,
        }

    start = frozenset()
    dist = {start: 0}
    prev = {}
    tie = itertools.count()
    pq = [(0, next(tie), start)]
    finalized = []  # list of (state, cost) popped and expanded
    expansions = 0
    best_goal_cost = None
    best_goal_state = None
    interrupted = False
    frontier_floor = 0

    while pq:
        cost, _, state = heapq.heappop(pq)
        if cost > dist.get(state, INF):
            continue
        if any(state <= fin_state and fin_cost <= cost
               for fin_state, fin_cost in finalized):
            continue  # dominated by an already-expanded superset state
        if expansion_budget is not None and expansions >= expansion_budget:
            interrupted = True
            frontier_floor = cost
            break
        finalized.append((state, cost))
        expansions += 1
        if outputs <= state:
            best_goal_cost = cost
            best_goal_state = state
            break  # first goal popped by Dijkstra is optimal
        for nid in sorted(needed - state):
            spec = nodes[nid]
            if not all(p in state for p in spec["parents"]):
                continue
            base = state | {nid}
            if sum(sizes[x] for x in base) <= memory_limit:
                kept_options = (base,)
            else:
                kept_options = _maximal_kept_sets(base, nid, sizes, memory_limit)
            for kept in kept_options:
                new_cost = cost + spec["cost"]
                if new_cost < dist.get(kept, INF):
                    dist[kept] = new_cost
                    prev[kept] = (state, nid)
                    heapq.heappush(pq, (new_cost, next(tie), kept))
    else:
        frontier_floor = None  # search space exhausted

    if best_goal_cost is None:
        # No goal popped; maybe goal states were generated but not expanded.
        for state, cost in dist.items():
            if outputs <= state and (best_goal_cost is None or cost < best_goal_cost):
                best_goal_cost = cost
                best_goal_state = state

    if interrupted:
        result = {
            "status": "interrupted",
            "reason": "expansion budget exhausted before proving optimality",
            "lower_bound": frontier_floor,
            "upper_bound": best_goal_cost,
            "expansions": expansions,
            "memory_limit": memory_limit,
        }
        if best_goal_state is not None:
            result["total_cost"] = best_goal_cost
            result["steps"] = _reconstruct(prev, best_goal_state, nodes, sizes)
            result["optimal"] = False
        return result

    if best_goal_state is None:
        return {
            "status": "infeasible",
            "reason": "no plan reaches the requested outputs within the memory limit",
            "memory_limit": memory_limit,
            "expansions": expansions,
        }

    return {
        "status": "optimal",
        "total_cost": best_goal_cost,
        "lower_bound": best_goal_cost,
        "upper_bound": best_goal_cost,
        "steps": _reconstruct(prev, best_goal_state, nodes, sizes),
        "expansions": expansions,
        "memory_limit": memory_limit,
    }


def _reconstruct(prev, goal_state, nodes, sizes):
    chain = []
    state = goal_state
    while state in prev:
        prev_state, nid = prev[state]
        chain.append((prev_state, nid, state))
        state = prev_state
    chain.reverse()
    steps = []
    for index, (prev_state, nid, kept) in enumerate(chain, start=1):
        spec = nodes[nid]
        steps.append({
            "step": index,
            "compute": nid,
            "cost": spec["cost"],
            "parents": [
                {"id": p, "size": sizes[p], "resident": p in prev_state}
                for p in spec["parents"]
            ],
            "peak_memory": peak_memory(nodes, nid),
            "memory_before": sum(sizes[x] for x in prev_state),
            "memory_after": sum(sizes[x] for x in kept),
            "cached_after": sorted(kept),
            "evicted": sorted(prev_state - kept),
        })
    return steps


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def load_problem(path):
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
    if not isinstance(data, dict):
        raise PlanError("top-level JSON value must be an object")
    if "memory_limit" not in data:
        raise PlanError("missing required key: memory_limit")
    nodes = validate_nodes(data.get("nodes", []))
    outputs = data.get("outputs")
    if not isinstance(outputs, list) or not outputs:
        raise PlanError("missing or empty required key: outputs (list of ids)")
    budget = data.get("expansion_budget")
    return nodes, outputs, data["memory_limit"], budget


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Minimum-cost bounded-memory recomputation planner "
                    "for DAG compute graphs.")
    parser.add_argument("input", help="path to the problem of the JSON problem file")
    parser.add_argument("-b", "--budget", type=int, default=None,
                        help="override expansion_budget from the input file")
    parser.add_argument("--compact", action="store_true",
                        help="emit compact single-line JSON")
    args = parser.parse_args(argv)

    try:
        nodes, outputs, memory_limit, budget = load_problem(args.input)
        if args.budget is not None:
            budget = args.budget
        result = solve(nodes, outputs, memory_limit, expansion_budget=budget)
    except PlanError as exc:
        print(json.dumps({"status": "error", "reason": str(exc)}))
        return 2

    indent = None if args.compact else 2
    print(json.dumps(result, indent=indent))
    return 0 if result["status"] == "optimal" else 1


if __name__ == "__main__":
    sys.exit(main())
