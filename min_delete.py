#!/usr/bin/env python3.11
"""Budgeted exact minimum rule deletion (minimum-weight feedback arc set).

A set of directed priority rules forms a directed graph.  When the graph
contains cycles, a minimum-total-cost set of rules (edges) must be deleted
so that the remaining graph is acyclic.  Every rule has a positive integer
cost.  Ties are broken by (1) fewest deleted rules, then (2) the
lexicographically smallest sorted list of deleted rule IDs.

The solver is an exact branch-and-bound with a node budget.  If the budget
is exhausted before the search completes, the best feasible solution found
so far is returned together with a valid lower bound on the optimum cost
(edge-disjoint cycle packing bound), and the result is flagged as not
proven optimal.

Standard library only; requires Python 3.11+.
"""
from __future__ import annotations

import argparse
import heapq
import json
import sys
from dataclasses import dataclass


class InputError(ValueError):
    """Raised when the input instance is malformed."""


@dataclass(frozen=True)
class Rule:
    id: str
    src: str
    dst: str
    cost: int


def parse_rules(data: object) -> list[Rule]:
    """Validate and parse the JSON input document into a list of rules."""
    if not isinstance(data, dict) or "rules" not in data:
        raise InputError('input must be a JSON object with a "rules" array')
    raw = data["rules"]
    if not isinstance(raw, list):
        raise InputError('"rules" must be an array')
    rules: list[Rule] = []
    seen: set[str] = set()
    for i, item in enumerate(raw):
        if not isinstance(item, dict):
            raise InputError(f"rule #{i}: must be an object")
        missing = {"id", "from", "to", "cost"} - item.keys()
        if missing:
            raise InputError(f"rule #{i}: missing field(s) {sorted(missing)}")
        rid, src, dst, cost = item["id"], item["from"], item["to"], item["cost"]
        if not isinstance(rid, str) or not rid:
            raise InputError(f"rule #{i}: 'id' must be a non-empty string")
        if rid in seen:
            raise InputError(f"duplicate rule id: {rid!r}")
        if not isinstance(src, str) or not src or not isinstance(dst, str) or not dst:
            raise InputError(f"rule {rid!r}: 'from'/'to' must be non-empty strings")
        if isinstance(cost, bool) or not isinstance(cost, int) or cost <= 0:
            raise InputError(f"rule {rid!r}: 'cost' must be a positive integer")
        seen.add(rid)
        rules.append(Rule(rid, src, dst, cost))
    return rules


class Graph:
    """Static graph view over the rules; edge i corresponds to rules[i]."""

    def __init__(self, rules: list[Rule]):
        self.rules = rules
        self.nodes = sorted({r.src for r in rules} | {r.dst for r in rules})
        self.adj: dict[str, list[int]] = {n: [] for n in self.nodes}
        for i, r in enumerate(rules):
            self.adj[r.src].append(i)


def find_cycle(graph: Graph, alive_mask: int) -> list[int] | None:
    """Return one cycle (as edge indices) in the subgraph kept by alive_mask.

    Iterative DFS (gray-stack) cycle detection; returns None if acyclic.
    Handles self-loops and parallel edges.
    """
    rules = graph.rules
    color = {n: 0 for n in graph.nodes}  # 0=white 1=gray 2=black
    parent_edge: dict[str, int] = {}
    for start in graph.nodes:
        if color[start]:
            continue
        color[start] = 1
        stack = [(start, iter(graph.adj[start]))]
        while stack:
            node, it = stack[-1]
            descended = False
            for ei in it:
                if not (alive_mask >> ei) & 1:
                    continue
                dst = rules[ei].dst
                if color[dst] == 0:
                    color[dst] = 1
                    parent_edge[dst] = ei
                    stack.append((dst, iter(graph.adj[dst])))
                    descended = True
                    break
                if color[dst] == 1:
                    cycle = [ei]
                    cur = node
                    while cur != dst:
                        pe = parent_edge[cur]
                        cycle.append(pe)
                        cur = rules[pe].src
                    return cycle
            if not descended:
                color[node] = 2
                stack.pop()
    return None


def lower_bound(graph: Graph, alive_mask: int) -> int:
    """Edge-disjoint cycle packing lower bound on the min deletion cost.

    Repeatedly finds a cycle, charges the cheapest edge on it, and removes
    that cycle's edges from consideration.  Every cycle must lose at least
    one edge costing at least its minimum edge cost, and the charged cycles
    are edge-disjoint, so the sum never overcounts: it is a valid lower
    bound on the optimum for the remaining graph.
    """
    total = 0
    mask = alive_mask
    rules = graph.rules
    while True:
        cycle = find_cycle(graph, mask)
        if cycle is None:
            return total
        total += min(rules[e].cost for e in cycle)
        for e in cycle:
            mask &= ~(1 << e)


class BudgetedSolver:
    """Exact branch-and-bound over cycles with a node-expansion budget."""

    def __init__(self, rules: list[Rule], budget: int):
        self.graph = Graph(rules)
        self.n = len(rules)
        self.budget = budget
        self.nodes_explored = 0
        self.exhausted = False
        # Incumbent: (cost, count, sorted ids tuple, deleted edge mask).
        self.best: tuple[int, int, tuple[str, ...], int] | None = None
        self.global_lb = 0

    @staticmethod
    def _key(cost: int, ids: tuple[str, ...]) -> tuple[int, int, tuple[str, ...]]:
        return (cost, len(ids), tuple(sorted(ids)))

    def _greedy_incumbent(self) -> None:
        """Feasible start: repeatedly delete the cheapest edge on a cycle."""
        rules = self.graph.rules
        mask = (1 << self.n) - 1
        deleted: list[str] = []
        cost = 0
        del_mask = 0
        while True:
            cycle = find_cycle(self.graph, mask)
            if cycle is None:
                break
            e = min(cycle, key=lambda i: (rules[i].cost, rules[i].id))
            mask &= ~(1 << e)
            del_mask |= 1 << e
            cost += rules[e].cost
            deleted.append(rules[e].id)
        self.best = (cost, len(deleted), tuple(sorted(deleted)), del_mask)

    def solve(self) -> None:
        self._greedy_incumbent()
        full = (1 << self.n) - 1
        self.global_lb = lower_bound(self.graph, full)
        self._search(full, 0, (), 0)

    def _search(self, mask: int, cost: int, ids: tuple[str, ...], del_mask: int) -> None:
        rules = self.graph.rules
        cycle = find_cycle(self.graph, mask)
        if cycle is None:
            key = self._key(cost, ids)
            assert self.best is not None
            if key < self.best[:3]:
                self.best = (cost, len(ids), tuple(sorted(ids)), del_mask)
            return
        bound = cost + lower_bound(self.graph, mask)
        assert self.best is not None
        best_cost, best_count = self.best[0], self.best[1]
        # Prune: cannot beat the incumbent on cost, or can only tie on cost
        # while already unable to tie on the deletion count.
        if bound > best_cost or (bound == best_cost and len(ids) + 1 > best_count):
            return
        if self.nodes_explored >= self.budget:
            self.exhausted = True
            if bound < self.global_lb:
                self.global_lb = bound
            return
        self.nodes_explored += 1
        for e in sorted(cycle, key=lambda i: (rules[i].cost, rules[i].id)):
            self._search(
                mask & ~(1 << e),
                cost + rules[e].cost,
                ids + (rules[e].id,),
                del_mask | (1 << e),
            )


def topological_order(graph: Graph, deleted_mask: int) -> list[str]:
    """Deterministic (lexicographic) Kahn topological order of the kept graph."""
    indeg = {n: 0 for n in graph.nodes}
    for i, r in enumerate(graph.rules):
        if (deleted_mask >> i) & 1:
            continue
        indeg[r.dst] += 1
    heap = [n for n in graph.nodes if indeg[n] == 0]
    heapq.heapify(heap)
    order: list[str] = []
    while heap:
        node = heapq.heappop(heap)
        order.append(node)
        for ei in graph.adj[node]:
            if (deleted_mask >> ei) & 1:
                continue
            dst = graph.rules[ei].dst
            indeg[dst] -= 1
            if indeg[dst] == 0:
                heapq.heappush(heap, dst)
    if len(order) != len(graph.nodes):
        raise RuntimeError("remaining graph still contains a cycle")
    return order


def verify_solution(
    rules: list[Rule], deleted_ids: list[str], topo_order: list[str]
) -> dict:
    """Independent verifier: checks a claimed solution from scratch.

    Uses its own Kahn-based acyclicity check (independent of the solver's
    DFS cycle finder).  Returns a dict with 'ok', 'errors' and 'cost'.
    """
    errors: list[str] = []
    by_id: dict[str, Rule] = {}
    for r in rules:
        if r.id in by_id:
            errors.append(f"duplicate rule id in instance: {r.id!r}")
        by_id[r.id] = r

    deleted_set: set[str] = set()
    for rid in deleted_ids:
        if rid not in by_id:
            errors.append(f"deleted id {rid!r} is not a known rule")
        elif rid in deleted_set:
            errors.append(f"rule {rid!r} deleted twice")
        deleted_set.add(rid)

    nodes = sorted({r.src for r in rules} | {r.dst for r in rules})
    if sorted(topo_order) != nodes:
        errors.append("topological order is not a permutation of the nodes")
    pos = {n: i for i, n in enumerate(topo_order)}

    kept = [r for r in rules if r.id not in deleted_set]
    if not errors:
        for r in kept:
            if not pos[r.src] < pos[r.dst]:
                errors.append(
                    f"order violates kept rule {r.id!r}: {r.src} -> {r.dst}"
                )
        # Independent acyclicity check via Kahn's algorithm.
        indeg = {n: 0 for n in nodes}
        adj: dict[str, list[str]] = {n: [] for n in nodes}
        for r in kept:
            indeg[r.dst] += 1
            adj[r.src].append(r.dst)
        ready = [n for n in nodes if indeg[n] == 0]
        seen = 0
        while ready:
            node = ready.pop()
            seen += 1
            for nxt in adj[node]:
                indeg[nxt] -= 1
                if indeg[nxt] == 0:
                    ready.append(nxt)
        if seen != len(nodes):
            errors.append("remaining graph still contains a cycle")

    cost = sum(by_id[rid].cost for rid in deleted_set if rid in by_id)
    return {"ok": not errors, "errors": errors, "cost": cost}


def solve_instance(rules: list[Rule], budget: int) -> dict:
    """Solve one instance; returns the full result dictionary."""
    solver = BudgetedSolver(rules, budget)
    solver.solve()
    assert solver.best is not None
    best_cost, best_count, best_ids, del_mask = solver.best
    order = topological_order(solver.graph, del_mask)
    optimal = not solver.exhausted
    return {
        "optimal": optimal,
        "budget_exhausted": solver.exhausted,
        "deleted": list(best_ids),
        "deleted_cost": best_cost,
        "deleted_count": best_count,
        "lower_bound": best_cost if optimal else solver.global_lb,
        "topological_order": order,
        "nodes_explored": solver.nodes_explored,
        "budget": budget,
    }


def _load_json(path: str) -> object:
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except OSError as exc:
        raise InputError(f"cannot read {path!r}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise InputError(f"invalid JSON in {path!r}: {exc}") from exc


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Budgeted exact minimum rule deletion "
        "(minimum-weight feedback arc set)."
    )
    parser.add_argument("input", help="instance JSON file ('-' for stdin)")
    parser.add_argument(
        "--budget",
        type=int,
        default=100_000,
        help="max branch-and-bound node expansions (default: 100000)",
    )
    parser.add_argument(
        "--verify",
        metavar="RESULT_JSON",
        help="verification mode: check a previously produced result file "
        "against the input instance",
    )
    parser.add_argument("--output", metavar="OUT", help="write result JSON here")
    args = parser.parse_args(argv)

    try:
        data = (
            json.load(sys.stdin)
            if args.input == "-"
            else _load_json(args.input)
        )
        rules = parse_rules(data)
        if args.budget < 0:
            raise InputError("--budget must be >= 0")

        if args.verify:
            claimed = _load_json(args.verify)
            if not isinstance(claimed, dict):
                raise InputError("result file must be a JSON object")
            report = verify_solution(
                rules,
                list(claimed.get("deleted", [])),
                list(claimed.get("topological_order", [])),
            )
            out = {"verified": report["ok"], **report}
            print(json.dumps(out, indent=2, ensure_ascii=False))
            return 0 if report["ok"] else 1

        result = solve_instance(rules, args.budget)
        check = verify_solution(
            rules, result["deleted"], result["topological_order"]
        )
        result["verified"] = check["ok"]
        if not check["ok"]:
            result["verification_errors"] = check["errors"]
        text = json.dumps(result, indent=2, ensure_ascii=False)
        if args.output:
            with open(args.output, "w", encoding="utf-8") as fh:
                fh.write(text + "\n")
        print(text)
        return 0 if result["verified"] else 1
    except InputError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
