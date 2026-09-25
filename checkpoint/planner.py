"""Checkpoint planning.

Chain graphs: exact DP that minimises total recompute cost under a memory
budget, plus an exhaustive-search reference used to prove optimality on
small instances.

General DAGs: a greedy heuristic plus a computable lower bound.

Cost model (matches the engine exactly)
---------------------------------------
Chain positions 0..n; x_0 is the graph input, x_i the output of node i.
Checkpoints persist through the whole backward pass.  During the backward
step of node j we need x_{j-1} (its input) and x_j (its output); any
missing activation is recomputed from the nearest earlier checkpoint and
recomputed activations are freed at the end of the step.  Hence, for a
segment between consecutive checkpoints i < k (k = n+1 denotes the chain
end, i.e. x_n is not stored either):

    cost(i, k) = sum_{s=i+1}^{k-1} c_s * (k - s + 1)          (k <= n)
    cost(i, n+1) = sum_{s=i+1}^{n}   c_s * (n - s + 1)

Peak memory = sum of checkpoint sizes + 2 * max activation size
(the recomputed frontier holds at most two activations at once)
+ workspace (gradient buffers, accounted separately by the caller).
"""
from __future__ import annotations

import math
from functools import lru_cache


def segment_cost(costs, i, k, n):
    """Recompute cost of the segment between checkpoints i and k."""
    if k == n + 1:
        return sum(costs[s] * (n - s + 1) for s in range(i + 1, n + 1))
    return sum(costs[s] * (k - s + 1) for s in range(i + 1, k))


def checkpoint_capacity(sizes, budget, workspace=0):
    """Bytes available for persistent checkpoints."""
    return budget - workspace - 2 * max(sizes)


def gradient_workspace(input_sizes, max_act):
    """Bytes the engine needs beyond checkpoints and the recompute
    frontier: input activations stay alive for the whole run, every
    input's gradient accumulates until the end, the current node's
    output gradient is live, and accumulation transiently holds the old
    buffer, the incoming gradient and the merged buffer.
    """
    return 2 * sum(input_sizes) + 2 * max(input_sizes) + max_act


def plan_chain_optimal(sizes, costs, budget, workspace=0, required=()):
    """Min-cost checkpoint set for a chain.  Returns (checkpoints, cost).

    sizes[i] = bytes of x_i (i in 0..n); costs[i] = recompute cost of the
    node producing x_i (costs[0] unused).  `required` positions are forced
    to be checkpoints (e.g. outputs of non-replayable side-effect nodes).
    Returns None if the budget cannot hold the required checkpoints.

    Optimality: dp(i, m) = min over next checkpoint k of
    segment_cost(i, k) + dp(k, m - size_k).  The cost of a segment depends
    only on its endpoints, so the subproblems are independent and the
    recurrence is exact (optimal substructure); the memoised recursion
    enumerates every feasible first-move choice, hence the global minimum.
    """
    n = len(sizes) - 1
    cap = checkpoint_capacity(sizes, budget, workspace)
    required = sorted(set(required) | {0})
    if cap < sum(sizes[r] for r in required):
        return None

    @lru_cache(maxsize=None)
    def dp(i, m):
        limit = n + 1
        for r in required:
            if r > i:
                limit = r
                break
        best_cost = math.inf
        best_k = None
        for k in range(i + 1, limit + 1):
            sz = sizes[k] if k <= n else 0
            if sz > m:
                continue
            c = segment_cost(costs, i, k, n)
            if k <= n:
                sub_cost, _ = dp(k, m - sz)
                if sub_cost == math.inf:
                    continue
                c += sub_cost
            if c < best_cost:
                best_cost, best_k = c, k
        return best_cost, best_k

    m0 = cap - sizes[0]
    if m0 < 0:
        return None
    total, k = dp(0, m0)
    if k is None:
        return None
    chosen = []
    i, m = 0, m0
    while k is not None and k <= n:
        chosen.append(k)
        m -= sizes[k]
        i = k
        _, k = dp(i, m)
    return [0] + chosen, total


def plan_chain_bruteforce(sizes, costs, budget, workspace=0, required=()):
    """Exhaustive-search reference; proves the DP optimal on small chains."""
    n = len(sizes) - 1
    cap = checkpoint_capacity(sizes, budget, workspace)
    required = set(required) | {0}
    optional = [i for i in range(1, n + 1) if i not in required]
    best = None
    best_cost = math.inf
    for mask in range(1 << len(optional)):
        sel = sorted(required | {optional[b] for b in range(len(optional))
                                 if mask >> b & 1})
        if sum(sizes[s] for s in sel) > cap:
            continue
        total = 0.0
        for a, b in zip(sel, sel[1:] + [n + 1]):
            total += segment_cost(costs, a, b, n)
        if total < best_cost:
            best_cost, best = total, sel
    if best is None:
        return None
    return best, best_cost


# ---------------------------------------------------------------------------
# general DAGs
# ---------------------------------------------------------------------------

def plan_dag_greedy(sizes, costs, budget, workspace=0, required=(),
                    descendants=None):
    """Greedy heuristic: store nodes with the best recompute-cost-per-byte
    score (weighted by descendant count) until the budget is exhausted."""
    cap = budget - workspace - 2 * max(sizes.values())
    chosen = set(required)
    used = sum(sizes[s] for s in chosen)
    if used > cap:
        return None
    descendants = descendants or {}
    scored = []
    for nm in sizes:
        if nm in chosen:
            continue
        weight = costs[nm] * (1 + descendants.get(nm, 0))
        scored.append((weight / max(sizes[nm], 1), nm))
    for _, nm in sorted(scored, reverse=True):
        if used + sizes[nm] <= cap:
            chosen.add(nm)
            used += sizes[nm]
    return chosen


def dag_lower_bound(sizes, costs, budget, workspace=0):
    """Lower bound on the total recompute cost of any DAG checkpoint plan.

    Every node that is not stored must be recomputed at least once, so the
    recompute cost of a plan is at least the sum of costs of unstored
    nodes.  Minimising that over all budget-feasible stored sets is a
    knapsack problem (maximise stored cost); its optimum is a valid lower
    bound because the true schedule may recompute unstored nodes more than
    once but never less.
    """
    cap = budget - workspace - 2 * max(sizes.values())
    cap = max(cap, 0)
    dp = [0.0] * (cap + 1)
    for nm, sz in sizes.items():
        c = costs[nm]
        for m in range(cap, sz - 1, -1):
            if dp[m - sz] + c > dp[m]:
                dp[m] = dp[m - sz] + c
    return sum(costs.values()) - max(dp)
