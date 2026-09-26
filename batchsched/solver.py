"""Exact batch scheduler: no external solvers, exact integer/rational math.

Model
-----
Each job belongs to a compatibility family, has a resource size, a processing
time, a deadline and a cost.  A batch may only contain jobs of one family
(incompatible jobs are never merged to save cost), its total size must fit the
capacity, and jobs are indivisible.  Batches run sequentially; a batch's
processing time is the maximum processing time of its jobs, and every job in
the batch completes when the batch completes.  Each started batch incurs the
fixed setup cost exactly once (per batch, not per job).  The goal is to
minimise total cost = setup_cost * #batches + sum(job costs) while meeting
every deadline.

Algorithm
---------
Exact dynamic programming over job subsets with Pareto (cost, time) fronts,
a greedy upper bound, and a per-family capacity lower bound for pruning.
A node budget bounds the search: if it is exhausted the result is reported
as FEASIBLE (best known solution, optimality not proven) or UNDECIDED
(no solution known), never silently as optimal.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from itertools import combinations
from typing import Optional

from .model import Instance

# Solve statuses
OPTIMAL = "optimal"            # proven minimum-cost feasible schedule
FEASIBLE = "feasible"          # feasible schedule found, optimality not proven
INFEASIBLE = "infeasible"      # proven: no feasible schedule exists
UNDECIDED = "undecided"        # search budget exhausted before any conclusion
# Budget statuses
BUDGET_MET = "budget_met"
BUDGET_EXCEEDED = "budget_exceeded"
BUDGET_UNDECIDED = "budget_undecided"

DEFAULT_NODE_LIMIT = 2_000_000


@dataclass
class SolveResult:
    status: str
    cost: Optional[Fraction]
    batches: Optional[list[list[str]]]  # job ids per batch, in schedule order
    nodes: int
    truncated: bool
    message: str = ""


class _Entry:
    """One Pareto point: schedule for `mask` using `batches` batches and
    `time` elapsed time, with parent pointers for reconstruction."""

    __slots__ = ("batches", "time", "mask", "batch_mask", "parent")

    def __init__(self, batches, time, mask, batch_mask, parent):
        self.batches = batches
        self.time = time
        self.mask = mask
        self.batch_mask = batch_mask
        self.parent = parent


def _enumerate_batches(inst: Instance) -> list[tuple[int, Fraction, Fraction]]:
    """All feasible batches as (job bitmask, processing time, min deadline)."""
    families: dict[str, list[int]] = {}
    for i, job in enumerate(inst.jobs):
        families.setdefault(job.family, []).append(i)
    batches: list[tuple[int, Fraction, Fraction]] = []
    for idxs in families.values():
        for r in range(1, len(idxs) + 1):
            for combo in combinations(idxs, r):
                size = sum((inst.jobs[i].size for i in combo), Fraction(0))
                if size > inst.capacity:
                    continue
                mask = 0
                for i in combo:
                    mask |= 1 << i
                ptime = max(inst.jobs[i].time for i in combo)
                dmin = min(inst.jobs[i].deadline for i in combo)
                batches.append((mask, ptime, dmin))
    return batches


def _lower_bound_batches(inst: Instance, remaining_mask: int) -> int:
    """Minimum extra batches needed for the remaining jobs (capacity only)."""
    totals: dict[str, Fraction] = {}
    for i, job in enumerate(inst.jobs):
        if remaining_mask >> i & 1:
            totals[job.family] = totals.get(job.family, Fraction(0)) + job.size
    return sum(math.ceil(total / inst.capacity) for total in totals.values())


def _greedy(inst: Instance) -> Optional[list[list[int]]]:
    """EDD-based greedy schedule; returns job-index batches or None."""
    remaining = set(range(len(inst.jobs)))
    t = Fraction(0)
    batches: list[list[int]] = []
    while remaining:
        pivot = min(remaining, key=lambda i: (inst.jobs[i].deadline, inst.jobs[i].jid))
        family = inst.jobs[pivot].family
        candidates = sorted(
            (i for i in remaining if inst.jobs[i].family == family),
            key=lambda i: (inst.jobs[i].deadline, inst.jobs[i].jid),
        )
        batch: list[int] = []
        size = Fraction(0)
        ptime = Fraction(0)
        for i in candidates:
            job = inst.jobs[i]
            new_size = size + job.size
            new_ptime = max(ptime, job.time)
            if new_size > inst.capacity:
                continue
            finish = t + new_ptime
            if job.deadline < finish:
                continue
            if any(inst.jobs[k].deadline < finish for k in batch):
                continue
            batch.append(i)
            size = new_size
            ptime = new_ptime
        if not batch:
            return None  # heuristic failed; the exact search decides
        t += ptime
        batches.append(batch)
        remaining.difference_update(batch)
    return batches


def _pareto_add(front: list[_Entry], entry: _Entry) -> bool:
    for e in front:
        if e.batches <= entry.batches and e.time <= entry.time:
            return False
    front[:] = [
        e for e in front
        if not (entry.batches <= e.batches and entry.time <= e.time)
    ]
    front.append(entry)
    return True


def _dp_search(
    inst: Instance,
    node_limit: int,
    batch_cap: Optional[int] = None,
    stop_at_first: bool = False,
    initial_best: Optional[int] = None,
) -> tuple[Optional[_Entry], Optional[int], int, bool]:
    """Subset DP.  Returns (best entry, best batch count, nodes, truncated).

    `batch_cap` prunes any partial schedule using more than that many batches
    (budget mode).  `stop_at_first` stops at the first complete schedule.
    """
    n = len(inst.jobs)
    batches = _enumerate_batches(inst)
    full = (1 << n) - 1
    best = initial_best
    best_entry: Optional[_Entry] = None
    root = _Entry(0, Fraction(0), 0, 0, None)
    fronts: dict[int, list[_Entry]] = {0: [root]}
    order = sorted(range(1 << n), key=lambda m: bin(m).count("1"))
    nodes = 0
    truncated = False

    for mask in order:
        front = fronts.get(mask)
        if not front:
            continue
        remaining = full ^ mask
        lb = _lower_bound_batches(inst, remaining)
        for entry in front:
            if best is not None and entry.batches + lb >= best:
                continue
            for bmask, ptime, dmin in batches:
                if bmask & mask:
                    continue
                t2 = entry.time + ptime
                if t2 > dmin:
                    continue
                b2 = entry.batches + 1
                if batch_cap is not None and b2 > batch_cap:
                    continue
                if best is not None and b2 >= best:
                    continue
                nodes += 1
                if nodes > node_limit:
                    truncated = True
                    break
                nmask = mask | bmask
                e2 = _Entry(b2, t2, nmask, bmask, entry)
                if nmask == full:
                    best = b2
                    best_entry = e2
                    if stop_at_first:
                        return best_entry, best, nodes, truncated
                else:
                    _pareto_add(fronts.setdefault(nmask, []), e2)
            if truncated:
                break
        if truncated:
            break
    return best_entry, best, nodes, truncated


def _reconstruct(inst: Instance, entry: _Entry) -> list[list[int]]:
    masks = []
    e: Optional[_Entry] = entry
    while e is not None and e.parent is not None:
        masks.append(e.batch_mask)
        e = e.parent
    masks.reverse()
    n = len(inst.jobs)
    return [[i for i in range(n) if bm >> i & 1] for bm in masks]


def _total_cost(inst: Instance, num_batches: int) -> Fraction:
    return inst.setup_cost * num_batches + inst.job_cost_sum


def solve(inst: Instance, node_limit: int = DEFAULT_NODE_LIMIT) -> SolveResult:
    """Minimise total cost subject to deadlines.  Exact when not truncated."""
    n = len(inst.jobs)
    if n == 0:
        return SolveResult(OPTIMAL, Fraction(0), [], 0, False, "empty instance")

    greedy_batches = _greedy(inst)
    initial_best = len(greedy_batches) if greedy_batches is not None else None
    best_entry, best, nodes, truncated = _dp_search(
        inst, node_limit, initial_best=initial_best
    )

    if best is None:
        if truncated:
            return SolveResult(
                UNDECIDED, None, None, nodes, True,
                "node limit exhausted before any feasible schedule was found",
            )
        return SolveResult(
            INFEASIBLE, None, None, nodes, False,
            "proven: no batch schedule meets all deadlines",
        )

    if best_entry is not None:
        batches_idx = _reconstruct(inst, best_entry)
    else:  # best solution came from the greedy bound
        batches_idx = greedy_batches
    batch_ids = [[inst.jobs[i].jid for i in batch] for batch in batches_idx]
    cost = _total_cost(inst, best)
    if truncated:
        return SolveResult(
            FEASIBLE, cost, batch_ids, nodes, True,
            "node limit exhausted: solution not proven optimal",
        )
    return SolveResult(OPTIMAL, cost, batch_ids, nodes, False, "proven optimal")


def decide_budget(
    inst: Instance, budget: Fraction, node_limit: int = DEFAULT_NODE_LIMIT
) -> SolveResult:
    """Decide whether the minimum total cost is <= budget.

    BUDGET_MET is certified by a concrete schedule within budget;
    BUDGET_EXCEEDED is only reported after a complete (exact) search;
    otherwise BUDGET_UNDECIDED.
    """
    n = len(inst.jobs)
    fixed = inst.job_cost_sum
    if fixed > budget:
        return SolveResult(
            BUDGET_EXCEEDED, None, None, 0, False,
            "job costs alone exceed the budget",
        )
    if inst.setup_cost == 0:
        allowed: Optional[int] = None
    else:
        allowed = math.floor((budget - fixed) / inst.setup_cost)
    if n == 0:
        return SolveResult(BUDGET_MET, fixed, [], 0, False, "empty instance")

    greedy_batches = _greedy(inst)
    if greedy_batches is not None and (
        allowed is None or len(greedy_batches) <= allowed
    ):
        ids = [[inst.jobs[i].jid for i in b] for b in greedy_batches]
        return SolveResult(
            BUDGET_MET, _total_cost(inst, len(greedy_batches)), ids, 0, False,
            "certified by a concrete schedule within budget",
        )

    best_entry, best, nodes, truncated = _dp_search(
        inst, node_limit, batch_cap=allowed, stop_at_first=True
    )
    if best_entry is not None and best is not None:
        ids = [[inst.jobs[i].jid for i in b] for b in _reconstruct(inst, best_entry)]
        return SolveResult(
            BUDGET_MET, _total_cost(inst, best), ids, nodes, truncated,
            "certified by a concrete schedule within budget",
        )
    if truncated:
        return SolveResult(
            BUDGET_UNDECIDED, None, None, nodes, True,
            "node limit exhausted: no schedule within budget found, "
            "and budget excess not proven",
        )
    return SolveResult(
        BUDGET_EXCEEDED, None, None, nodes, False,
        "proven: every feasible schedule exceeds the budget",
    )


def verify_schedule(inst: Instance, batches_idx: list[list[int]]) -> bool:
    """Check all hard constraints for a concrete schedule (job indices)."""
    seen: list[int] = []
    t = Fraction(0)
    for batch in batches_idx:
        if not batch:
            return False
        if len({inst.jobs[i].family for i in batch}) != 1:
            return False  # incompatible jobs mixed
        size = sum((inst.jobs[i].size for i in batch), Fraction(0))
        if size > inst.capacity:
            return False
        t += max(inst.jobs[i].time for i in batch)
        for i in batch:
            if inst.jobs[i].deadline < t:
                return False
        seen.extend(batch)
    return sorted(seen) == list(range(len(inst.jobs)))
