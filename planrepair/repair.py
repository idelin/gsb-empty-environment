"""Public repair API.

Given an originally feasible plan, a disruption (added tasks and/or failed
resources) and the current time ``now``, compute a repaired plan that

* freezes the executed prefix (executed tasks keep their placement and are
  never re-run in the future),
* places every non-executed / added task at a start time >= ``now``,
* lexicographically minimises (number of moved tasks, total cost),
* leaves unaffected tasks in their original position whenever possible.

Result statuses are kept strictly separate:

* ``OPTIMAL``           -- search space exhausted within budget; the returned
                           plan is certified optimal for (moves, cost).
* ``FEASIBLE``          -- a feasible repaired plan was found and is returned,
                           but the node budget ran out before optimality could
                           be certified.
* ``BUDGET_UNDECIDED``  -- the node budget ran out before any feasible repair
                           was found; whether a repair exists is undecided and
                           the original plan is retained.
* ``INFEASIBLE``        -- certified that no feasible repair exists; the
                           original plan is retained together with conflict
                           evidence.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .model import Disruption, Placement, Plan, Q, Resource, Task
from .solver import build_slots, common_grid_denominator, search

DEFAULT_NODE_BUDGET = 200_000


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"
    INFEASIBLE = "infeasible"


@dataclass(frozen=True)
class Change:
    """One entry of the change list (变更清单)."""

    kind: str  # "frozen" | "kept" | "moved" | "added"
    tid: str
    old: Optional[Placement]
    new: Optional[Placement]


@dataclass(frozen=True)
class ConflictEvidence:
    """Why a repair is impossible: per-task reasons, or a joint-contention note."""

    tid: str  # task id, or "*" for a joint (capacity contention) record
    reasons: Tuple[str, ...]


@dataclass
class SearchStats:
    nodes: int
    node_budget: int
    aborted: bool
    forced_moves: int  # lower bound: tasks whose original slot is certainly gone


@dataclass
class RepairResult:
    status: Status
    plan: Plan  # repaired plan, or the original plan when no repair is returned
    changes: Tuple[Change, ...]
    moved_count: int
    total_cost: Optional[Fraction]
    conflicts: Tuple[ConflictEvidence, ...]
    stats: SearchStats

    @property
    def ok(self) -> bool:
        return self.status in (Status.OPTIMAL, Status.FEASIBLE)


def _validate_original_plan(tasks: Dict[str, Task], resources: Dict[str, Resource],
                            plan: Plan, now: Fraction) -> None:
    unknown = set(plan.assignments) - set(tasks)
    if unknown:
        raise ValueError(f"plan references unknown tasks: {sorted(unknown)}")
    per_resource: Dict[str, List[Tuple[Fraction, Fraction]]] = {}
    for tid, placement in plan.assignments.items():
        task = tasks[tid]
        resource = resources.get(placement.rid)
        if resource is None:
            raise ValueError(f"original plan places {tid!r} on unknown resource {placement.rid!r}")
        if placement.rid not in task.costs:
            raise ValueError(f"task {tid!r} is not eligible for resource {placement.rid!r}")
        start, end = placement.start, placement.start + task.duration
        if start < task.release:
            raise ValueError(f"original plan starts {tid!r} before its release time")
        if task.deadline is not None and end > task.deadline:
            raise ValueError(f"original plan ends {tid!r} after its deadline")
        if not any(w.start <= start and end <= w.end for w in resource.windows):
            raise ValueError(f"original plan places {tid!r} outside any window of {placement.rid!r}")
        if tid in plan.executed and end > now:
            raise ValueError(f"executed task {tid!r} ends after now={now}")
        per_resource.setdefault(placement.rid, []).append((start, end))
    for rid, intervals in per_resource.items():
        capacity = resources[rid].capacity
        points = sorted({p for iv in intervals for p in iv})
        for a, b in zip(points, points[1:]):
            if sum(1 for s, e in intervals if s <= a and e >= b) > capacity:
                raise ValueError(f"original plan exceeds capacity of resource {rid!r}")


def _conflict_evidence(task: Task, resources: Dict[str, Resource],
                       failed: frozenset, now: Fraction) -> ConflictEvidence:
    reasons: List[str] = []
    eligible = set(task.costs)
    for rid in sorted(eligible & set(failed)):
        reasons.append(f"resource {rid!r} failed")
    for rid in sorted(eligible - set(resources) - set(failed)):
        reasons.append(f"resource {rid!r} does not exist")
    for rid in sorted(eligible & set(resources)):
        res = resources[rid]
        fits_somewhere = False
        for w in res.windows:
            lo = max(now, task.release, w.start)
            hard_end = w.end if task.deadline is None else min(w.end, task.deadline)
            if lo <= hard_end - task.duration:
                fits_somewhere = True
                break
        if not fits_somewhere:
            reasons.append(
                f"no window of resource {rid!r} fits duration {task.duration} "
                f"within release={task.release}, deadline={task.deadline}, now={now}"
            )
    if not reasons:
        reasons.append("no eligible resource available")
    return ConflictEvidence(task.tid, tuple(reasons))


def repair(resources, tasks, plan: Plan, disruption: Optional[Disruption] = None,
           *, now=0, node_budget: int = DEFAULT_NODE_BUDGET) -> RepairResult:
    """Repair ``plan`` after ``disruption``.  See module docstring for semantics."""
    now = Q(now)
    disruption = disruption or Disruption()
    if node_budget < 0:
        raise ValueError("node_budget must be >= 0")

    resources_by_id: Dict[str, Resource] = {}
    for r in resources:
        if r.rid in resources_by_id:
            raise ValueError(f"duplicate resource id {r.rid!r}")
        resources_by_id[r.rid] = r
    task_map: Dict[str, Task] = {}
    for t in tasks:
        if t.tid in task_map:
            raise ValueError(f"duplicate task id {t.tid!r}")
        task_map[t.tid] = t
    added_map: Dict[str, Task] = {}
    for t in disruption.added_tasks:
        if t.tid in task_map or t.tid in added_map:
            raise ValueError(f"added task id {t.tid!r} collides with an existing task")
        added_map[t.tid] = t
    unknown_failed = disruption.failed_resources - set(resources_by_id)
    if unknown_failed:
        raise ValueError(f"failed resources do not exist: {sorted(unknown_failed)}")

    _validate_original_plan(task_map, resources_by_id, plan, now)

    available = {rid: r for rid, r in resources_by_id.items()
                 if rid not in disruption.failed_resources}

    original_tids = [tid for tid in plan.assignments if tid not in plan.executed]
    to_schedule = original_tids + list(added_map)
    all_tasks = {**task_map, **added_map}

    # A task is a forced mover when its original slot is certainly gone:
    # its resource failed, or its original start lies in the past (< now).
    forced = {
        tid for tid in original_tids
        if plan.assignments[tid].rid in disruption.failed_resources
        or plan.assignments[tid].start < now
    }

    grid = common_grid_denominator(
        [now]
        + [w.start for r in resources_by_id.values() for w in r.windows]
        + [w.end for r in resources_by_id.values() for w in r.windows]
        + [t.duration for t in all_tasks.values()]
        + [t.release for t in all_tasks.values()]
        + [t.deadline for t in all_tasks.values() if t.deadline is not None]
        + [p.start for p in plan.assignments.values()]
    )

    slots_by_tid: Dict[str, list] = {}
    for tid in to_schedule:
        original = plan.assignments.get(tid)  # None for added tasks
        slots_by_tid[tid] = build_slots(all_tasks[tid], original, available, now, grid)

    # Fast path: a task with zero candidate slots proves infeasibility.
    hopeless = [tid for tid in to_schedule if not slots_by_tid[tid]]
    if hopeless:
        conflicts = tuple(
            _conflict_evidence(all_tasks[tid], available, disruption.failed_resources, now)
            for tid in sorted(hopeless)
        )
        return RepairResult(
            status=Status.INFEASIBLE,
            plan=plan,
            changes=(),
            moved_count=0,
            total_cost=None,
            conflicts=conflicts,
            stats=SearchStats(0, node_budget, False, len(forced)),
        )

    frozen_intervals: Dict[str, List[Tuple[Fraction, Fraction]]] = {}
    for tid in plan.executed:
        placement = plan.assignments[tid]
        if placement.rid in available:
            frozen_intervals.setdefault(placement.rid, []).append(
                (placement.start, placement.start + task_map[tid].duration)
            )

    # Fail-first task order (fewest candidates first), deterministic by tid.
    order = sorted(to_schedule, key=lambda tid: (len(slots_by_tid[tid]), tid))
    forced_suffix = [0] * (len(order) + 1)
    for i in range(len(order) - 1, -1, -1):
        forced_suffix[i] = forced_suffix[i + 1] + (1 if order[i] in forced else 0)

    durations = {tid: all_tasks[tid].duration for tid in to_schedule}
    outcome = search(
        order, slots_by_tid, set(original_tids), durations, available,
        frozen_intervals, forced_suffix, node_budget,
    )
    stats = SearchStats(outcome.nodes, node_budget, outcome.aborted, len(forced))

    if outcome.incumbent is None:
        if outcome.aborted:
            return RepairResult(Status.BUDGET_UNDECIDED, plan, (), 0, None, (), stats)
        conflicts: List[ConflictEvidence] = [
            ConflictEvidence(
                "*",
                (
                    "exhaustive search proved no feasible joint assignment "
                    f"(capacity contention); tasks={sorted(to_schedule)}, "
                    f"available resources={sorted(available)}",
                ),
            )
        ]
        return RepairResult(Status.INFEASIBLE, plan, (), 0, None, tuple(conflicts), stats)

    moved, total_cost = outcome.incumbent_key
    new_assignments = dict(plan.assignments)
    for tid, slot in outcome.incumbent.items():
        new_assignments[tid] = Placement(slot.rid, slot.start)
    new_plan = Plan(new_assignments, plan.executed)

    changes: List[Change] = []
    for tid in sorted(plan.assignments):
        old = plan.assignments[tid]
        if tid in plan.executed:
            changes.append(Change("frozen", tid, old, old))
        else:
            new = new_assignments[tid]
            kind = "kept" if new == old else "moved"
            changes.append(Change(kind, tid, old, new))
    for tid in sorted(added_map):
        changes.append(Change("added", tid, None, new_assignments[tid]))

    status = Status.FEASIBLE if outcome.aborted else Status.OPTIMAL
    return RepairResult(status, new_plan, tuple(changes), moved, total_cost, (), stats)
