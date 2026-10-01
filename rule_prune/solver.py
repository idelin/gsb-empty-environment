"""Exact branch-and-bound solver for budgeted minimum rule deletion.

A rule set induces a directed precedence graph: each rule is a directed
edge ``src -> dst`` with a positive integer deletion cost.  When the graph
contains directed cycles, rules must be deleted so the remaining graph is
acyclic.  The objective is to minimise the total deletion cost; ties are
broken by fewer deleted rules, then by the lexicographically smallest
sorted list of deleted rule ids.

The solver is an exact depth-first branch-and-bound.  A valid lower bound
is obtained from a packing of edge-disjoint cycles: every directed cycle
must lose at least one edge, and edge-disjoint cycles must lose *distinct*
edges, so the sum of the per-cycle minimum edge cost never exceeds the
optimum.  This correctly handles edges shared by several cycles (the cost
of a shared edge is counted once, not once per cycle).

When the node budget (or time limit) is exhausted before the search tree
is closed, the solver returns the best feasible solution found so far
together with a valid global lower bound (the minimum lower bound over
all still-open search nodes; the bound is monotone along every branch).
"""
from __future__ import annotations

import heapq
import time
from dataclasses import dataclass


@dataclass(frozen=True)
class Rule:
    id: str
    cost: int
    src: str
    dst: str


def parse_instance(data):
    """Validate a decoded JSON instance and return a list of Rule."""
    if not isinstance(data, dict) or not isinstance(data.get("rules"), list):
        raise ValueError("instance must be a JSON object with a 'rules' array")
    rules = []
    seen = set()
    for entry in data["rules"]:
        if not isinstance(entry, dict):
            raise ValueError(f"rule entry must be an object, got {entry!r}")
        try:
            rid = entry["id"]
            cost = entry["cost"]
            src = entry["src"]
            dst = entry["dst"]
        except KeyError as exc:
            raise ValueError(f"rule entry missing field {exc}") from exc
        if not isinstance(rid, str) or not rid:
            raise ValueError(f"rule id must be a non-empty string, got {rid!r}")
        if not isinstance(cost, int) or isinstance(cost, bool) or cost <= 0:
            raise ValueError(
                f"rule {rid!r}: cost must be a positive integer, got {cost!r}"
            )
        for field_name, node in (("src", src), ("dst", dst)):
            if not isinstance(node, str) or not node:
                raise ValueError(
                    f"rule {rid!r}: {field_name} must be a non-empty string"
                )
        if rid in seen:
            raise ValueError(f"duplicate rule id {rid!r}")
        seen.add(rid)
        rules.append(Rule(rid, cost, src, dst))
    return rules


def build_adjacency(rules, deleted):
    """Adjacency of the graph after removing the deleted rule ids.

    Maps every node to a list of (successor, rule_id) pairs.  Every node
    appears as a key, even without outgoing edges.
    """
    adj = {}
    for r in rules:
        adj.setdefault(r.src, [])
        adj.setdefault(r.dst, [])
        if r.id not in deleted:
            adj[r.src].append((r.dst, r.id))
    return adj


def find_cycle(adj):
    """Return a directed cycle as a list of rule ids, or None if acyclic."""
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {node: WHITE for node in adj}
    for start in sorted(adj):
        if color[start] != WHITE:
            continue
        color[start] = GRAY
        path_nodes = [start]
        path_edges = []  # path_edges[i] leads into path_nodes[i + 1]
        stack = [(start, iter(sorted(adj[start])))]
        while stack:
            node, it = stack[-1]
            item = next(it, None)
            if item is None:
                color[node] = BLACK
                stack.pop()
                path_nodes.pop()
                if path_edges:
                    path_edges.pop()
                continue
            nxt, eid = item
            if nxt == node:
                return [eid]  # self-loop
            state = color.get(nxt, WHITE)
            if state == GRAY:
                idx = path_nodes.index(nxt)
                return path_edges[idx:] + [eid]
            if state == WHITE:
                color[nxt] = GRAY
                path_nodes.append(nxt)
                path_edges.append(eid)
                stack.append((nxt, iter(sorted(adj.get(nxt, [])))))
    return None


def lower_bound_packing(rules, deleted):
    """Edge-disjoint cycle packing lower bound for the remaining graph.

    Returns (bound, certificate) where certificate is a list of cycles
    (each a list of rule ids) that are pairwise edge-disjoint in the
    remaining graph.  The bound is the sum of the cheapest edge cost per
    cycle and never exceeds the optimum additional deletion cost, because
    the optimum must delete at least one *distinct* edge per cycle.
    """
    cost_of = {r.id: r.cost for r in rules}
    removed = set(deleted)
    certificate = []
    total = 0
    while True:
        cycle = find_cycle(build_adjacency(rules, removed))
        if cycle is None:
            return total, certificate
        certificate.append(cycle)
        total += min(cost_of[e] for e in cycle)
        # Remove every edge of the cycle so the next cycle found is
        # edge-disjoint from all cycles recorded so far.
        removed.update(cycle)


def topological_order(rules, deleted):
    """Deterministic (lexicographic Kahn) topological order of the rest."""
    adj = {}
    indeg = {}
    for r in rules:
        adj.setdefault(r.src, [])
        adj.setdefault(r.dst, [])
        indeg.setdefault(r.src, 0)
        indeg.setdefault(r.dst, 0)
    for r in rules:
        if r.id in deleted:
            continue
        adj[r.src].append(r.dst)
        indeg[r.dst] += 1
    heap = [n for n, d in indeg.items() if d == 0]
    heapq.heapify(heap)
    order = []
    while heap:
        node = heapq.heappop(heap)
        order.append(node)
        for succ in adj[node]:
            indeg[succ] -= 1
            if indeg[succ] == 0:
                heapq.heappush(heap, succ)
    if len(order) != len(indeg):
        raise ValueError("remaining graph still contains a cycle")
    return order


@dataclass
class SolveResult:
    status: str  # "optimal" | "feasible"
    deleted: list
    deleted_cost: int
    topological_order: list
    lower_bound: int
    certified_lower_bound: int
    lower_bound_certificate: list
    nodes_expanded: int
    budget: int
    elapsed_seconds: float

    def to_dict(self):
        return {
            "status": self.status,
            "deleted": self.deleted,
            "deleted_cost": self.deleted_cost,
            "topological_order": self.topological_order,
            "lower_bound": self.lower_bound,
            "certified_lower_bound": self.certified_lower_bound,
            "lower_bound_certificate": self.lower_bound_certificate,
            "stats": {
                "nodes_expanded": self.nodes_expanded,
                "budget": self.budget,
                "elapsed_seconds": self.elapsed_seconds,
            },
        }


def _solution_key(cost, deleted):
    return (cost, len(deleted), sorted(deleted))


def _greedy_solution(rules, cost_of):
    """Feasible starting incumbent: break cycles by their cheapest edge."""
    deleted = set()
    while True:
        cycle = find_cycle(build_adjacency(rules, deleted))
        if cycle is None:
            return frozenset(deleted)
        deleted.add(min(cycle, key=lambda e: (cost_of[e], e)))


def solve(rules, budget=100_000, time_limit=None):
    """Exact branch-and-bound with a node budget.

    budget: maximum number of search-tree nodes to expand.
    time_limit: optional wall-clock seconds; also stops the search early.

    Returns a SolveResult.  status is "optimal" only when the search tree
    was fully explored; otherwise "feasible" and `lower_bound` is a valid
    global lower bound on the optimum cost.
    """
    cost_of = {r.id: r.cost for r in rules}

    best_deleted = _greedy_solution(rules, cost_of)
    best_cost = sum(cost_of[e] for e in best_deleted)

    certified_lb, certificate = lower_bound_packing(rules, frozenset())

    stack = [(frozenset(), certified_lb)]
    nodes_expanded = 0
    start = time.monotonic()
    exhausted = False

    while stack:
        if nodes_expanded >= budget or (
            time_limit is not None and time.monotonic() - start >= time_limit
        ):
            exhausted = True
            break
        current, current_lb = stack.pop()
        if current_lb > best_cost:
            continue
        if current_lb == best_cost and len(current) >= len(best_deleted):
            # Any completion costs >= best_cost and deletes >= as many
            # rules; an equal-cost, equal-count completion would have to
            # be exactly `current`, which is already accounted for.
            continue
        nodes_expanded += 1
        cycle = find_cycle(build_adjacency(rules, current))
        if cycle is None:
            cost = sum(cost_of[e] for e in current)
            if _solution_key(cost, current) < _solution_key(best_cost, best_deleted):
                best_cost, best_deleted = cost, current
            continue
        # Branch: at least one edge of this cycle must be deleted.
        for eid in sorted(cycle, key=lambda e: (cost_of[e], e), reverse=True):
            child = current | {eid}
            child_cost = sum(cost_of[e] for e in child)
            if child_cost > best_cost:
                continue
            pack_lb, _ = lower_bound_packing(rules, child)
            child_lb = child_cost + pack_lb
            if child_lb > best_cost:
                continue
            if child_lb == best_cost and len(child) >= len(best_deleted):
                continue
            stack.append((child, child_lb))

    if exhausted:
        # Valid global lower bound: the packing bound is monotone along
        # every branch, so the minimum bound among open nodes bounds the
        # optimum of the whole unexplored subtree set.
        lower_bound = min([lb for _, lb in stack] + [best_cost])
        status = "feasible"
    else:
        lower_bound = best_cost
        status = "optimal"

    return SolveResult(
        status=status,
        deleted=sorted(best_deleted),
        deleted_cost=best_cost,
        topological_order=topological_order(rules, best_deleted),
        lower_bound=lower_bound,
        certified_lower_bound=certified_lb,
        lower_bound_certificate=certificate,
        nodes_expanded=nodes_expanded,
        budget=budget,
        elapsed_seconds=round(time.monotonic() - start, 6),
    )
