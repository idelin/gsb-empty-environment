"""Optimal checkpoint selection on a chain (sequential) computation graph.

Model
-----
Nodes ``0..n`` are executed in order.  Node 0 is the input and is always kept.
Each node ``i`` has an activation size ``a[i]`` (memory units) and a recompute
cost ``c[i]`` (time units).  A *plan* is a set of checkpoints ``C`` (always
containing 0): activations of nodes in ``C`` persist through the whole backward
pass.  During backward, when activation ``j not in C`` is needed, it is
recomputed forward from the nearest checkpoint ``k < j``, materialising
activations ``k+1 .. j`` transiently.

* persistent memory  = sum of a[k] for k in C
* transient memory   = max over segments (k, m] between consecutive checkpoints
                       (with a virtual segment from the last checkpoint to n)
                       of sum of a[t] for recomputed t in that segment
* peak memory        = persistent + transient  (must be <= budget B)
* recompute cost     = sum of c[t] over all non-checkpoint nodes t
                       (each node is recomputed at most once in this model)

DP (exact, pseudo-polynomial in the budget)
-------------------------------------------
We sweep checkpoints left to right and keep, for every candidate *last
checkpoint* ``i``, the Pareto frontier of states ``(persistent, transient,
cost)``: a state is dominated if another state at the same node has
``persistent' <= persistent``, ``transient' <= transient`` and
``cost' <= cost``.  Transition ``i -> m`` (make ``m`` the next checkpoint):

    seg_mem  = sum a[t]  for i < t < m          (transient of the new segment)
    seg_cost = sum c[t]  for i < t < m
    persistent' = persistent + a[m]
    transient'  = max(transient, seg_mem)
    cost'       = cost + seg_cost
    feasible iff persistent' + transient' <= B

The answer closes the final segment from each last checkpoint ``i`` to ``n``:
``cost + sum_{t>i} c[t]`` feasible iff ``persistent + max(transient,
sum_{t>i} a[t]) <= B``.

Correctness: every plan corresponds to exactly one path 0 = i0 < i1 < ... < ik
through the DP (its checkpoint set), and the DP's feasibility test and cost for
that path equal the plan's peak-memory test and recompute cost by the
definitions above.  Because dominated states can never lead to a strictly
better final answer (all updates are monotone in each component), pruning
preserves the optimum.  Hence the DP enumerates the optimum over all feasible
plans.  ``brute_force_chain`` verifies this by enumerating all ``2^n`` subsets
for small ``n``; the unit tests check both agree on many random instances.
"""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class ChainPlan:
    checkpoints: list          # sorted node indices that persist (includes 0)
    recompute_cost: float      # total recompute cost of the plan
    peak_memory: float         # persistent + transient peak
    feasible: bool


def _prefix_sums(values):
    pref = [0.0]
    for v in values:
        pref.append(pref[-1] + v)
    return pref


def solve_chain(sizes, costs, budget):
    """Return the minimum-recompute-cost feasible :class:`ChainPlan`.

    ``sizes``/``costs`` are lists of length ``n + 1`` (index 0 is the input,
    which is always checkpointed and occupies ``sizes[0]`` memory).
    Raises ``ValueError`` if no feasible plan exists.
    """
    n = len(sizes) - 1
    a_pref = _prefix_sums(sizes)
    c_pref = _prefix_sums(costs)

    def seg(lo, hi):  # sum over lo < t < hi  (open interval)
        return a_pref[hi] - a_pref[lo + 1] if hi > lo + 1 else 0.0

    def seg_cost(lo, hi):
        return c_pref[hi] - c_pref[lo + 1] if hi > lo + 1 else 0.0

    # frontier[i]: list of (persistent, transient, cost, checkpoints_tuple)
    frontier = {0: [(sizes[0], 0.0, 0.0, (0,))]}

    for i in range(0, n + 1):
        if i not in frontier:
            continue
        for m in range(i + 1, n + 1):
            t_seg = seg(i, m)
            for pers, tran, cost, ckpts in frontier[i]:
                new_pers = pers + sizes[m]
                new_tran = max(tran, t_seg)
                if new_pers + new_tran > budget:
                    continue
                new_cost = cost + seg_cost(i, m)
                state = (new_pers, new_tran, new_cost, ckpts + (m,))
                lst = frontier.setdefault(m, [])
                # prune: drop states dominated by `state`, skip if dominated
                dominated = False
                kept = []
                for s in lst:
                    if s[0] <= state[0] and s[1] <= state[1] and s[2] <= state[2]:
                        dominated = True
                        break
                    if not (state[0] <= s[0] and state[1] <= s[1] and state[2] <= s[2]):
                        kept.append(s)
                if not dominated:
                    kept.append(state)
                    frontier[m] = kept

    best = None
    for i, states in frontier.items():
        tail_mem = a_pref[n + 1] - a_pref[i + 1] if n > i else 0.0
        tail_cost = c_pref[n + 1] - c_pref[i + 1] if n > i else 0.0
        for pers, tran, cost, ckpts in states:
            peak = pers + max(tran, tail_mem)
            if peak > budget:
                continue
            total = cost + tail_cost
            if best is None or total < best.recompute_cost:
                best = ChainPlan(list(ckpts), total, peak, True)
    if best is None:
        raise ValueError("no feasible checkpoint plan within the memory budget")
    return best


def evaluate_plan(sizes, costs, budget, checkpoints):
    """Independent evaluation of an explicit checkpoint set (used by brute force)."""
    ckpts = sorted(set(checkpoints) | {0})
    persistent = sum(sizes[k] for k in ckpts)
    bounds = ckpts + [len(sizes)]  # virtual end marker at n+1
    transient = 0.0
    cost = 0.0
    for lo, hi in zip(bounds, bounds[1:]):
        hi_excl = hi if hi == len(sizes) else hi  # segment covers lo < t < hi
        end = hi if hi == len(sizes) else hi
        seg_mem = sum(sizes[t] for t in range(lo + 1, end))
        seg_cost = sum(costs[t] for t in range(lo + 1, end))
        transient = max(transient, seg_mem)
        cost += seg_cost
    peak = persistent + transient
    return ChainPlan(ckpts, cost, peak, peak <= budget)


def brute_force_chain(sizes, costs, budget):
    """Exhaustively enumerate all checkpoint subsets; returns the best plan."""
    n = len(sizes) - 1
    best = None
    for mask in range(1 << n):
        ckpts = [0] + [i for i in range(1, n + 1) if mask & (1 << (i - 1))]
        plan = evaluate_plan(sizes, costs, budget, ckpts)
        if plan.feasible and (best is None or plan.recompute_cost < best.recompute_cost):
            best = plan
    if best is None:
        raise ValueError("no feasible checkpoint plan within the memory budget")
    return best
