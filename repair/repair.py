"""Plan repair: minimise moved tasks first, then minimise cost.

Pipeline per machine (machines are independent, so the lexicographic
objective decomposes exactly):

1.  The executed prefix is frozen; executed tasks are never moved and
    never re-run in the future.
2.  Existing unexecuted tasks overlapping a resource failure are forced
    movers; all others are candidates to stay exactly where they are.
3.  Iterative deepening over the number k of moved tasks: for each k,
    every subset of "stayer" tasks of the required size is tried and the
    freed tasks (forced movers + chosen stayers + new tasks) are placed
    optimally by the exact single-machine optimiser.  The first feasible
    k is the provable minimum number of moved tasks; the best cost found
    at that level is the cost optimum for that k.

Budgets bound the search.  The reported status separates the three
decision states: OPTIMAL (search space exhausted), FEASIBLE (a repair
was found but not proven optimal within budget) and BUDGET_UNDECIDED
(budget exhausted before any feasible repair was confirmed).  When no
feasible repair can exist (frozen conflict with a failure, or not
enough remaining capacity), the original plan is kept and conflict
evidence is returned.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from itertools import combinations
from typing import Dict, List, Optional, Tuple

from .model import Failure, Scenario, Task, fmt_number
from .scheduler import PlacementItem, compute_gaps, optimize_placement


class Status(str, Enum):
    OPTIMAL = "OPTIMAL"                      # proven optimal repair
    FEASIBLE = "FEASIBLE"                    # feasible, optimality unproven
    BUDGET_UNDECIDED = "BUDGET_UNDECIDED"    # budget exhausted, unknown
    INFEASIBLE = "INFEASIBLE"                # provably no feasible repair


@dataclass(frozen=True)
class Change:
    task: str
    machine: str
    kind: str                       # "moved" | "scheduled"
    old_start: Optional[Fraction]   # None for newly added tasks
    new_start: Fraction

    def to_dict(self) -> dict:
        return {
            "task": self.task,
            "machine": self.machine,
            "kind": self.kind,
            "old_start": fmt_number(self.old_start),
            "new_start": fmt_number(self.new_start),
        }


@dataclass(frozen=True)
class Evidence:
    kind: str
    message: str
    details: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {"kind": self.kind, "message": self.message,
                "details": {k: fmt_number(v) if isinstance(v, Fraction) else v
                            for k, v in self.details.items()}}


@dataclass
class MachineOutcome:
    machine: str
    status: Status
    moves: int = 0
    cost: Optional[Fraction] = None
    starts: Dict[str, Fraction] = field(default_factory=dict)
    evidence: List[Evidence] = field(default_factory=list)
    subset_nodes: int = 0
    placement_nodes: int = 0
    note: str = ""

    def to_dict(self) -> dict:
        return {
            "status": self.status.value,
            "moves": self.moves,
            "cost": fmt_number(self.cost),
            "subset_nodes": self.subset_nodes,
            "placement_nodes": self.placement_nodes,
            "note": self.note,
        }


@dataclass
class RepairResult:
    status: Status
    kept_original_plan: bool
    moves: Optional[int] = None
    cost: Optional[Fraction] = None
    changes: List[Change] = field(default_factory=list)
    evidence: List[Evidence] = field(default_factory=list)
    machines: Dict[str, MachineOutcome] = field(default_factory=dict)
    notes: List[str] = field(default_factory=list)

    def to_dict(self) -> dict:
        return {
            "status": self.status.value,
            "kept_original_plan": self.kept_original_plan,
            "moves": self.moves,
            "cost": fmt_number(self.cost),
            "changes": [c.to_dict() for c in self.changes],
            "evidence": [e.to_dict() for e in self.evidence],
            "machines": {m: o.to_dict() for m, o in self.machines.items()},
            "notes": self.notes,
        }


def _overlaps(start: Fraction, duration: Fraction,
              interval: Tuple[Fraction, Optional[Fraction]]) -> bool:
    o_start, o_end = interval
    return (o_end is None or start < o_end) and o_start < start + duration


def validate(scenario: Scenario) -> None:
    """Reject inconsistent inputs (the original plan must be feasible)."""
    seen = set()
    for task in list(scenario.tasks) + list(scenario.new_tasks):
        if task.id in seen:
            raise ValueError(f"duplicate task id {task.id!r}")
        seen.add(task.id)
        if task.duration <= 0:
            raise ValueError(f"task {task.id!r}: duration must be positive")
    for task in scenario.tasks:
        if not task.executed and task.start < scenario.now:
            raise ValueError(
                f"task {task.id!r} starts at {task.start} before now="
                f"{scenario.now} but is not marked executed")
    for failure in scenario.failures:
        if failure.end is not None and failure.end <= failure.start:
            raise ValueError(f"failure on {failure.machine!r}: empty interval")
    by_machine: Dict[str, List[Task]] = {}
    for task in scenario.tasks:
        by_machine.setdefault(task.machine, []).append(task)
    for machine, tasks in by_machine.items():
        tasks.sort(key=lambda t: t.start)
        for first, second in zip(tasks, tasks[1:]):
            if first.end > second.start:
                raise ValueError(
                    f"original plan infeasible: tasks {first.id!r} and "
                    f"{second.id!r} overlap on machine {machine!r}")


def _repair_machine(machine: str,
                    existing: List[Task],
                    new_tasks: List[Task],
                    failures: List[Failure],
                    now: Fraction,
                    subset_budget: int,
                    placement_budget: int) -> MachineOutcome:
    frozen = [t for t in existing if t.executed]
    future = [t for t in existing if not t.executed]
    obstacles: List[Tuple[Fraction, Optional[Fraction]]] = [
        (f.start, f.end) for f in failures
    ] + [(t.start, t.end) for t in frozen]

    forced, stayers = [], []
    for task in future:
        if any(_overlaps(task.start, task.duration, ob) for ob in obstacles):
            forced.append(task)
        else:
            stayers.append(task)

    subset_nodes = 0
    placement_nodes = 0
    for extra in range(len(stayers) + 1):
        level_best = None  # (cost, starts)
        level_complete = True
        for subset in combinations(stayers, extra):
            subset_nodes += 1
            if subset_nodes > subset_budget:
                level_complete = False
                break
            moved = forced + list(subset)
            stayed = [t for t in stayers if t not in subset]
            fixed = obstacles + [(t.start, t.end) for t in stayed]
            gaps = compute_gaps(fixed, now)
            items = [PlacementItem(t.id, t.duration, t.start) for t in moved]
            items += [PlacementItem(t.id, t.duration, Fraction(0))
                      for t in new_tasks]
            result = optimize_placement(items, gaps, placement_budget)
            placement_nodes += result.nodes
            if not result.complete:
                level_complete = False
            if result.feasible and (level_best is None
                                    or result.cost < level_best[0]):
                level_best = (result.cost, result.starts)
        if level_best is not None:
            status = Status.OPTIMAL if level_complete else Status.FEASIBLE
            note = "" if level_complete else (
                "budget exhausted before this move level was fully "
                "explored; cost is a bound, not proven optimal")
            return MachineOutcome(
                machine=machine, status=status, moves=len(forced) + extra,
                cost=level_best[0], starts=level_best[1],
                subset_nodes=subset_nodes, placement_nodes=placement_nodes,
                note=note)
        if not level_complete:
            return MachineOutcome(
                machine=machine, status=Status.BUDGET_UNDECIDED,
                subset_nodes=subset_nodes, placement_nodes=placement_nodes,
                note="budget exhausted before feasibility could be decided")
    # Every level was fully explored and none was feasible: the machine
    # cannot absorb its tasks (permanent shutdown / capacity shortage).
    required = sum((t.duration for t in forced + list(new_tasks)),
                   Fraction(0))
    gaps = compute_gaps(obstacles, now)
    capacity = None
    if all(g.end is not None for g in gaps):
        capacity = sum((g.end - g.start for g in gaps), Fraction(0))
    evidence = [Evidence(
        kind="insufficient_capacity",
        message=(f"machine {machine!r}: no feasible repair; "
                 f"{len(forced)} task(s) displaced by failures plus "
                 f"{len(new_tasks)} new task(s) cannot be placed"),
        details={"machine": machine,
                 "required_duration": required,
                 "remaining_capacity": capacity,
                 "displaced_tasks": [t.id for t in forced],
                 "new_tasks": [t.id for t in new_tasks]})]
    return MachineOutcome(machine=machine, status=Status.INFEASIBLE,
                          evidence=evidence, subset_nodes=subset_nodes,
                          placement_nodes=placement_nodes)


def repair(scenario: Scenario) -> RepairResult:
    """Repair the plan of a scenario; see module docstring for the rules."""
    validate(scenario)

    # Frozen executed prefix vs. resource failures: cannot be repaired,
    # because executed tasks may neither move nor be re-run.
    frozen_evidence: List[Evidence] = []
    for task in scenario.tasks:
        if not task.executed:
            continue
        for failure in scenario.failures:
            if failure.machine != task.machine:
                continue
            if _overlaps(task.start, task.duration,
                         (failure.start, failure.end)):
                frozen_evidence.append(Evidence(
                    kind="frozen_conflict",
                    message=(f"executed task {task.id!r} on machine "
                             f"{task.machine!r} overlaps resource failure "
                             f"[{failure.start}, "
                             f"{failure.end if failure.end is not None else 'inf'})"
                             " and cannot be moved or re-run"),
                    details={"task": task.id, "machine": task.machine,
                             "task_start": task.start, "task_end": task.end,
                             "failure_start": failure.start,
                             "failure_end": failure.end}))
    if frozen_evidence:
        return RepairResult(
            status=Status.INFEASIBLE, kept_original_plan=True,
            evidence=frozen_evidence,
            notes=["executed prefix conflicts with a resource failure; "
                   "original plan kept unchanged"])

    machines = sorted({t.machine for t in scenario.tasks}
                      | {t.machine for t in scenario.new_tasks})
    outcomes: Dict[str, MachineOutcome] = {}
    for machine in machines:
        existing = [t for t in scenario.tasks if t.machine == machine]
        added = [t for t in scenario.new_tasks if t.machine == machine]
        failures = [f for f in scenario.failures if f.machine == machine]
        outcomes[machine] = _repair_machine(
            machine, existing, added, failures, scenario.now,
            scenario.subset_budget, scenario.placement_budget)

    statuses = {o.status for o in outcomes.values()}
    if Status.INFEASIBLE in statuses:
        evidence = [e for o in outcomes.values() for e in o.evidence]
        return RepairResult(
            status=Status.INFEASIBLE, kept_original_plan=True,
            evidence=evidence, machines=outcomes,
            notes=["no feasible repair exists; original plan kept unchanged"])
    if Status.BUDGET_UNDECIDED in statuses:
        undecided = sorted(m for m, o in outcomes.items()
                           if o.status is Status.BUDGET_UNDECIDED)
        return RepairResult(
            status=Status.BUDGET_UNDECIDED, kept_original_plan=True,
            machines=outcomes,
            notes=["budget exhausted before a feasible repair was proven; "
                   "original plan kept unchanged",
                   f"undecided machine(s): {', '.join(undecided)}"])

    # All machines repaired: build the change list.
    by_id = {t.id: t for t in list(scenario.tasks) + list(scenario.new_tasks)}
    changes: List[Change] = []
    total_cost = Fraction(0)
    for machine, outcome in outcomes.items():
        total_cost += outcome.cost
        for task_id, new_start in outcome.starts.items():
            task = by_id[task_id]
            if task.is_new:
                changes.append(Change(task_id, machine, "scheduled",
                                      None, new_start))
            elif new_start != task.start:
                changes.append(Change(task_id, machine, "moved",
                                      task.start, new_start))
    changes.sort(key=lambda c: (c.machine, c.new_start, c.task))
    moves = sum(1 for c in changes if c.kind == "moved")
    status = (Status.OPTIMAL if statuses == {Status.OPTIMAL}
              else Status.FEASIBLE)
    notes = []
    if status is Status.FEASIBLE:
        notes.append("repair found but not proven optimal within budget")
    return RepairResult(status=status, kept_original_plan=False,
                        moves=moves, cost=total_cost, changes=changes,
                        machines=outcomes, notes=notes)
