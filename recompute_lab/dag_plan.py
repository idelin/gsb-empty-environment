"""Checkpoint planning for general DAGs: a greedy heuristic plus a lower bound.

For a DAG, recomputing one node may require recomputing a whole ancestor cone,
and a node can be recomputed several times, so the chain DP does not apply.
We provide:

* ``greedy_dag_plan`` -- repeatedly checkpoint the node with the best
  saved-recompute-cost / memory ratio, keeping the plan feasible under an
  event-level memory simulation (see ``simulator.py``).
* ``lower_bound_cost`` -- a relaxation: pretend every node is recomputed at
  most once and transient recompute memory is free.  Then the best any plan
  could do is a 0/1 knapsack: choose checkpoints maximising saved cost
  ``sum c[i]`` subject to ``sum a[i] <= B``.  The true optimum can only be
  worse (DAGs recompute nodes multiple times and pay transient memory), so
  ``total_cost - knapsack(B)`` is a valid lower bound on the optimal
  recompute cost of any feasible plan.
"""
from __future__ import annotations

from dataclasses import dataclass

from simulator import simulate_plan


@dataclass
class DagPlan:
    checkpoints: frozenset
    recompute_cost: float
    peak_memory: float
    feasible: bool
    lower_bound: float  # valid lower bound on the optimum recompute cost


def _ancestor_cone(node, parents):
    """All strict ancestors of ``node`` (transitive)."""
    seen = set()
    stack = list(parents[node])
    while stack:
        cur = stack.pop()
        if cur in seen:
            continue
        seen.add(cur)
        stack.extend(parents[cur])
    return seen


def estimate_cost_and_peak(order, parents, sizes, costs, checkpoints, budget):
    """Event-level simulation of a candidate plan (delegates to simulator)."""
    return simulate_plan(order, parents, sizes, costs, checkpoints, budget)


def greedy_dag_plan(order, parents, sizes, costs, budget):
    """Greedy checkpoint selection.

    ``order`` is a topological order of node ids; ``parents[v]`` lists the
    direct predecessors.  Sources (no parents) are always treated as
    checkpoints (they are inputs and cannot be recomputed).
    """
    sources = frozenset(v for v in order if not parents[v])
    checkpoints = set(sources)

    def current():
        return simulate_plan(order, parents, sizes, costs, checkpoints, budget)

    # Phase 1: reach feasibility.  Adding checkpoints can *reduce* the peak
    # (it shrinks recompute cones), so greedily add the checkpoint that
    # lowers the peak most until the plan fits the budget.
    while not current().feasible:
        cur = current()
        best_key, best_node = None, None
        for v in order:
            if v in checkpoints:
                continue
            trial = simulate_plan(
                order, parents, sizes, costs, checkpoints | {v}, budget
            )
            key = (trial.peak_memory, trial.recompute_cost)
            if key < (cur.peak_memory, cur.recompute_cost) and (
                best_key is None or key < best_key
            ):
                best_key, best_node = key, v
        if best_node is None:
            raise ValueError("no feasible checkpoint plan within the memory budget")
        checkpoints.add(best_node)

    # Phase 2: while slack remains, add the checkpoint with the best
    # recompute-cost saving that keeps the plan feasible.
    improved = True
    while improved:
        improved = False
        best_gain, best_node = 0.0, None
        cur = current()
        for v in order:
            if v in checkpoints:
                continue
            trial = simulate_plan(
                order, parents, sizes, costs, checkpoints | {v}, budget
            )
            if not trial.feasible:
                continue
            gain = cur.recompute_cost - trial.recompute_cost
            if gain > best_gain + 1e-12:
                best_gain, best_node = gain, v
        if best_node is not None:
            checkpoints.add(best_node)
            improved = True

    final = current()
    lb = lower_bound_cost(order, sizes, costs, budget, sources)
    return DagPlan(
        frozenset(checkpoints),
        final.recompute_cost,
        final.peak_memory,
        final.feasible,
        lb,
    )


def lower_bound_cost(order, sizes, costs, budget, sources=()):
    """Knapsack relaxation lower bound on the optimal recompute cost.

    Solves max sum c[v] over checkpoint sets with sum a[v] <= budget via exact
    DP over the (integer) budget, then returns ``total_cost - max_saved``.
    Sources are forced checkpoints: their memory is subtracted first.
    """
    forced_mem = sum(sizes[v] for v in sources)
    remaining = int(round(budget - forced_mem))
    if remaining < 0:
        return 0.0
    items = [v for v in order if v not in sources]
    # dp[m] = max saved cost using exactly <= m memory
    dp = [0.0] * (remaining + 1)
    for v in items:
        a = int(round(sizes[v]))
        c = costs[v]
        for m in range(remaining, a - 1, -1):
            if dp[m - a] + c > dp[m]:
                dp[m] = dp[m - a] + c
    total = sum(costs[v] for v in items)
    return total - max(dp)
