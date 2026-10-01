#!/usr/bin/env python3.11
"""Bounded-cache recomputation planner for DAG computation graphs.

Each node has an integer compute cost and an integer result size.
Executing a node requires all of its parent results to be resident in
memory, and the node result itself must also fit.  A fixed memory limit
applies at all times.  Cached results may be evicted at zero cost and
recomputed later (paying the compute cost again).

The planner performs an exact Dijkstra search over materialization
states (the set of cached node results) and returns a minimum total
compute cost step sequence that leaves every requested target resident.
An optional expansion budget interrupts the search while preserving a
certified lower bound on the optimum.

Pure Python 3.11 standard library.
"""

from __future__ import annotations

import argparse
import heapq
import itertools
import json
import sys
from dataclasses import dataclass
from typing import Optional


class SpecError(ValueError):
    """Raised when the input specification is invalid."""


@dataclass(frozen=True)
class Node:
    name: str
    cost: int
    size: int
    parents: tuple[str, ...]


@dataclass
class Graph:
    nodes: dict[str, Node]
    memory_limit: int
    targets: tuple[str, ...]

    def size_of(self, name: str) -> int:
        return self.nodes[name].size


# ---------------------------------------------------------------------------
# Input parsing / validation
# ---------------------------------------------------------------------------

def _require_int(value, what: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise SpecError(f"{what} must be an integer, got {value!r}")
    return value


def parse_spec(data: dict) -> Graph:
    if not isinstance(data, dict):
        raise SpecError("top-level spec must be a JSON object")
    if "memory_limit" not in data:
        raise SpecError("missing required field 'memory_limit'")
    memory_limit = _require_int(data["memory_limit"], "memory_limit")
    if memory_limit < 0:
        raise SpecError("memory_limit must be >= 0")

    raw_nodes = data.get("nodes", [])
    if not isinstance(raw_nodes, list):
        raise SpecError("'nodes' must be a list")
    nodes: dict[str, Node] = {}
    for entry in raw_nodes:
        if not isinstance(entry, dict):
            raise SpecError("each node must be an object")
        name = entry.get("id")
        if not isinstance(name, str) or not name:
            raise SpecError(f"node id must be a non-empty string, got {name!r}")
        if name in nodes:
            raise SpecError(f"duplicate node id {name!r}")
        cost = _require_int(entry.get("cost"), f"node {name!r} cost")
        size = _require_int(entry.get("size"), f"node {name!r} size")
        if cost < 0:
            raise SpecError(f"node {name!r}: cost must be >= 0")
        if size < 0:
            raise SpecError(f"node {name!r}: size must be >= 0")
        parents = entry.get("parents", [])
        if not isinstance(parents, list) or not all(isinstance(p, str) for p in parents):
            raise SpecError(f"node {name!r}: parents must be a list of node ids")
        nodes[name] = Node(name=name, cost=cost, size=size,
                           parents=tuple(sorted(set(parents))))

    for node in nodes.values():
        for parent in node.parents:
            if parent not in nodes:
                raise SpecError(f"node {node.name!r}: unknown parent {parent!r}")

    targets = data.get("targets", [])
    if not isinstance(targets, list) or not all(isinstance(t, str) for t in targets):
        raise SpecError("'targets' must be a list of node ids")
    for target in targets:
        if target not in nodes:
            raise SpecError(f"unknown target {target!r}")

    graph = Graph(nodes=nodes, memory_limit=memory_limit,
                  targets=tuple(sorted(set(targets))))
    _check_acyclic(graph)
    return graph


def _check_acyclic(graph: Graph) -> None:
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {name: WHITE for name in graph.nodes}

    def visit(name: str, stack: list[str]) -> None:
        color[name] = GRAY
        stack.append(name)
        for parent in graph.nodes[name].parents:
            if color[parent] == GRAY:
                cycle = " -> ".join(stack[stack.index(parent):] + [parent])
                raise SpecError(f"graph must be acyclic; found cycle: {cycle}")
            if color[parent] == WHITE:
                visit(parent, stack)
        stack.pop()
        color[name] = BLACK

    for name in graph.nodes:
        if color[name] == WHITE:
            visit(name, [])


def needed_nodes(graph: Graph) -> set[str]:
    """Targets plus all of their transitive ancestors."""
    needed: set[str] = set()
    stack = list(graph.targets)
    while stack:
        name = stack.pop()
        if name in needed:
            continue
        needed.add(name)
        stack.extend(graph.nodes[name].parents)
    return needed


def static_violations(graph: Graph, needed: set[str]) -> list[str]:
    """Nodes whose computation can never fit in memory.

    Computing v requires every parent result resident plus room for the
    result of v itself; parents may never be dropped mid-computation.
    """
    violations = []
    for name in sorted(needed):
        node = graph.nodes[name]
        required = node.size + sum(graph.nodes[p].size for p in node.parents)
        if required > graph.memory_limit:
            violations.append(
                f"node {name!r} needs {required} units "
                f"(size {node.size} + parents "
                f"{sum(graph.nodes[p].size for p in node.parents)}) "
                f"> memory_limit {graph.memory_limit}"
            )
    return violations


# ---------------------------------------------------------------------------
# Exact state-space search (Dijkstra over cached-result sets)
# ---------------------------------------------------------------------------

def solve(graph: Graph, expansion_budget: Optional[int] = None) -> dict:
    """Search for a minimum-total-compute-cost plan.

    Returns a result dict with status one of:
      - "optimal":     proven minimum cost plan found
      - "infeasible":  no plan can exist under the memory limit
      - "interrupted": expansion budget exhausted; lower_bound is a
                       certified lower bound on the optimal total cost
    """
    needed = needed_nodes(graph)
    violations = static_violations(graph, needed)
    if violations:
        return {
            "status": "infeasible",
            "reason": "node plus resident parents exceeds memory_limit; "
                      "parents may not be dropped during computation",
            "violations": violations,
            "total_cost": None,
            "lower_bound": None,
            "expansions": 0,
            "steps": [],
        }

    targets = frozenset(graph.targets)
    sizes = {name: node.size for name, node in graph.nodes.items()}
    computable = [node for node in graph.nodes.values() if node.name in needed]

    def memory_of(state: frozenset) -> int:
        return sum(sizes[name] for name in state)

    start = frozenset()
    dist: dict[frozenset, int] = {start: 0}
    prev: dict[frozenset, tuple[frozenset, tuple]] = {}
    counter = itertools.count()
    queue: list[tuple[int, int, frozenset]] = [(0, next(counter), start)]
    expansions = 0

    def relax(state: frozenset, new_state: frozenset, new_cost: int, action: tuple) -> None:
        if new_cost < dist.get(new_state, sys.maxsize):
            dist[new_state] = new_cost
            prev[new_state] = (state, action)
            heapq.heappush(queue, (new_cost, next(counter), new_state))

    while queue:
        cost, _, state = heapq.heappop(queue)
        if cost > dist.get(state, sys.maxsize):
            continue  # stale heap entry
        if targets <= state:
            steps = _reconstruct(graph, prev, state)
            return {
                "status": "optimal",
                "total_cost": cost,
                "lower_bound": cost,
                "expansions": expansions,
                "steps": steps,
            }
        if expansion_budget is not None and expansions >= expansion_budget:
            return {
                "status": "interrupted",
                "reason": "expansion budget exhausted before goal was reached",
                "total_cost": None,
                "lower_bound": cost,
                "bound_note": "lower_bound is the minimum frontier cost; the "
                              "optimal total cost is >= lower_bound",
                "expansions": expansions,
                "steps": [],
            }
        expansions += 1

        # Zero-cost evictions: any cached result may be dropped freely.
        for name in state:
            relax(state, state - {name}, cost, ("evict", name))

        # Compute a needed node: all parents must be resident and the
        # result must fit alongside everything currently cached.
        used = memory_of(state)
        for node in computable:
            if node.name in state:
                continue
            if not set(node.parents) <= state:
                continue
            if used + node.size > graph.memory_limit:
                continue
            relax(state, state | {node.name}, cost + node.cost,
                  ("compute", node.name))

    return {
        "status": "infeasible",
        "reason": "state space exhausted: targets cannot all be resident "
                  "simultaneously under the memory limit",
        "total_cost": None,
        "lower_bound": None,
        "expansions": expansions,
        "steps": [],
    }


def _reconstruct(graph: Graph, prev: dict, goal: frozenset) -> list[dict]:
    chain = []
    state = goal
    while state in prev:
        state, action = prev[state]
        chain.append(action)
    chain.reverse()

    steps = []
    cache: set[str] = set()
    for index, action in enumerate(chain, start=1):
        kind, name = action
        if kind == "compute":
            node = graph.nodes[name]
            parents = list(node.parents)
            resident = all(p in cache for p in parents)
            cache.add(name)
            steps.append({
                "step": index,
                "action": "compute",
                "node": name,
                "cost": node.cost,
                "parents": parents,
                "parents_resident": resident,
                "memory_after": sum(graph.size_of(n) for n in cache),
                "cache_after": sorted(cache),
            })
        else:  # evict
            cache.discard(name)
            steps.append({
                "step": index,
                "action": "evict",
                "node": name,
                "memory_after": sum(graph.size_of(n) for n in cache),
                "cache_after": sorted(cache),
            })
    return steps


# ---------------------------------------------------------------------------
# Plan verification (dependency evidence check)
# ---------------------------------------------------------------------------

def verify_plan(graph: Graph, steps: list[dict]) -> dict:
    """Replay a plan, checking every memory and dependency constraint."""
    cache: set[str] = set()
    total_cost = 0

    def memory() -> int:
        return sum(graph.size_of(n) for n in cache)

    for index, step in enumerate(steps, start=1):
        action = step.get("action")
        name = step.get("node")
        if action not in ("compute", "evict") or name not in graph.nodes:
            return {"ok": False, "total_cost": None,
                    "error": f"step {index}: invalid action/node {step!r}"}
        if action == "compute":
            node = graph.nodes[name]
            missing = [p for p in node.parents if p not in cache]
            if missing:
                return {"ok": False, "total_cost": None,
                        "error": f"step {index}: compute {name!r} without "
                                 f"resident parents {missing}"}
            if memory() + node.size > graph.memory_limit:
                return {"ok": False, "total_cost": None,
                        "error": f"step {index}: computing {name!r} would "
                                 f"exceed memory_limit "
                                 f"({memory()}+{node.size} > {graph.memory_limit})"}
            cache.add(name)
            total_cost += node.cost
        else:
            if name not in cache:
                return {"ok": False, "total_cost": None,
                        "error": f"step {index}: evict {name!r} not in cache"}
            cache.discard(name)
        if "memory_after" in step and step["memory_after"] != memory():
            return {"ok": False, "total_cost": None,
                    "error": f"step {index}: declared memory_after "
                             f"{step['memory_after']} != actual {memory()}"}

    missing_targets = [t for t in graph.targets if t not in cache]
    if missing_targets:
        return {"ok": False, "total_cost": total_cost,
                "error": f"targets not resident at end: {missing_targets}"}
    return {"ok": True, "total_cost": total_cost, "error": None}


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _load_json(path: str):
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)


def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        prog="cacheplan",
        description="Bounded-cache recomputation planner for DAG computation "
                    "graphs (exact state search, Python 3.11 stdlib).")
    sub = parser.add_subparsers(dest="command", required=True)

    solve_parser = sub.add_parser("solve", help="compute an optimal plan")
    solve_parser.add_argument("spec", help="path to the problem spec JSON")
    solve_parser.add_argument("--budget", type=int, default=None,
                              help="maximum number of state expansions; on "
                                   "exhaustion the search stops and reports a "
                                   "certified lower bound")

    verify_parser = sub.add_parser("verify", help="verify a plan against a spec")
    verify_parser.add_argument("spec", help="path to the problem spec JSON")
    verify_parser.add_argument("plan", help="path to a solve-output JSON file")

    args = parser.parse_args(argv)

    try:
        graph = parse_spec(_load_json(args.spec))
    except (SpecError, json.JSONDecodeError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    if args.command == "solve":
        if args.budget is not None and args.budget < 0:
            print("error: --budget must be >= 0", file=sys.stderr)
            return 2
        result = solve(graph, expansion_budget=args.budget)
        print(json.dumps(result, indent=2))
        return 0 if result["status"] == "optimal" else 1

    # verify
    try:
        plan_data = _load_json(args.plan)
    except (json.JSONDecodeError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    steps = plan_data.get("steps", plan_data if isinstance(plan_data, list) else [])
    outcome = verify_plan(graph, steps)
    print(json.dumps(outcome, indent=2))
    return 0 if outcome["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
