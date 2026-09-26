"""Event-driven preemptive uniprocessor EDF scheduler (Python 3.11, stdlib only).

Features
--------
- Exact arithmetic on synthetic integer/rational data via ``fractions.Fraction``.
- Event-driven simulation: time jumps between releases and completions, never
  advances tick-by-tick.
- Stable tie-breaking: simultaneous release + identical deadline -> lower task
  ID runs first.
- Zero-workload tasks complete immediately at their release time.
- Tasks never execute before their release time.
- Infeasibility is proven with an overload time window (processor-demand
  evidence): an interval [a, b] whose total demand exceeds its capacity b - a.
- Results are separated into three statuses: FEASIBLE (which, since preemptive
  EDF is optimal on a uniprocessor, also certifies optimality), INFEASIBLE
  (with window evidence), and UNDECIDED (event budget exhausted).
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from typing import Iterable


def _frac(value: int | Fraction) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid numeric parameter")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"expected int or Fraction, got {type(value).__name__}")


@dataclass(frozen=True)
class Task:
    """A single job: released at ``release``, needs ``work`` CPU units, due at ``deadline``."""

    task_id: int
    release: int | Fraction
    work: int | Fraction
    deadline: int | Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "release", _frac(self.release))
        object.__setattr__(self, "work", _frac(self.work))
        object.__setattr__(self, "deadline", _frac(self.deadline))
        if self.work < 0:
            raise ValueError(f"task {self.task_id}: negative workload")
        if self.deadline < self.release:
            raise ValueError(f"task {self.task_id}: deadline before release")


@dataclass(frozen=True)
class Segment:
    """One contiguous execution slice [start, end) of a task."""

    task_id: int
    start: Fraction
    end: Fraction


@dataclass(frozen=True)
class OverloadWindow:
    """Evidence that no uniprocessor schedule can be feasible.

    Tasks released at or after ``start`` and due at or before ``end`` demand
    ``demand`` CPU units, but the interval only offers ``capacity`` units.
    """

    start: Fraction
    end: Fraction
    demand: Fraction
    capacity: Fraction
    task_ids: tuple[int, ...]

    @property
    def excess(self) -> Fraction:
        return self.demand - self.capacity

    def __str__(self) -> str:
        return (
            f"overload window [{self.start}, {self.end}]: "
            f"demand {self.demand} > capacity {self.capacity} "
            f"(excess {self.excess}), tasks {list(self.task_ids)}"
        )


class Status(Enum):
    FEASIBLE = "feasible"        # all deadlines met; EDF is optimal here
    INFEASIBLE = "infeasible"    # deadline missed; overload window attached
    UNDECIDED = "undecided"      # event budget exhausted before completion


@dataclass
class ScheduleResult:
    status: Status
    segments: list[Segment]
    completions: dict[int, Fraction]
    missed: list[int]
    evidence: OverloadWindow | None
    events_used: int

    @property
    def optimal(self) -> bool:
        """Preemptive EDF is optimal on a uniprocessor: a feasible EDF schedule
        means no algorithm can do better, and an infeasible EDF outcome (backed
        by an overload window) means no algorithm could have succeeded."""
        return self.status in (Status.FEASIBLE, Status.INFEASIBLE)


def find_overload_window(tasks: Iterable[Task]) -> OverloadWindow | None:
    """Processor-demand check over all [release, deadline] windows.

    Returns the window with the largest demand excess, or None if every window
    is satisfiable (which, for preemptive uniprocessor EDF, certifies
    feasibility).
    """
    tasks = list(tasks)
    releases = sorted({t.release for t in tasks})
    deadlines = sorted({t.deadline for t in tasks})
    best: OverloadWindow | None = None
    for a in releases:
        for b in deadlines:
            if b < a:
                continue
            members = [t for t in tasks if t.release >= a and t.deadline <= b]
            demand = sum((t.work for t in members), Fraction(0))
            capacity = b - a
            if demand > capacity:
                window = OverloadWindow(
                    start=a,
                    end=b,
                    demand=demand,
                    capacity=capacity,
                    task_ids=tuple(sorted(t.task_id for t in members)),
                )
                if best is None or window.excess > best.excess:
                    best = window
    return best


def simulate(tasks: Iterable[Task], event_budget: int | None = None) -> ScheduleResult:
    """Run event-driven preemptive EDF.

    ``event_budget`` caps the number of processed events (execution slices);
    when the budget is exhausted the result is UNDECIDED with the partial
    schedule computed so far.
    """
    task_list = list(tasks)
    ids = [t.task_id for t in task_list]
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate task IDs")
    if event_budget is not None and event_budget < 0:
        raise ValueError("event_budget must be non-negative or None")

    # Pending releases ordered by (release, deadline, id) for determinism.
    by_id = {t.task_id: t for t in task_list}
    pending = sorted(task_list, key=lambda t: (t.release, t.deadline, t.task_id))
    next_idx = 0
    # Ready heap keyed by (deadline, release, id): EDF with stable tie-break.
    ready: list[tuple[Fraction, Fraction, int]] = []
    remaining: dict[int, Fraction] = {}

    segments: list[Segment] = []
    completions: dict[int, Fraction] = {}
    missed: list[int] = []
    events = 0
    now = Fraction(0)

    while next_idx < len(pending) or ready:
        if not ready:
            # Jump directly to the next release; nothing can run earlier.
            now = max(now, pending[next_idx].release)
        # Admit every task released by ``now`` (batch keeps ties stable).
        while next_idx < len(pending) and pending[next_idx].release <= now:
            task = pending[next_idx]
            next_idx += 1
            if task.work == 0:
                # Zero workload completes immediately at its release.
                completions[task.task_id] = now
                if now > task.deadline:
                    missed.append(task.task_id)
            else:
                remaining[task.task_id] = task.work
                heapq.heappush(ready, (task.deadline, task.release, task.task_id))
        if not ready:
            continue

        _, _, current = heapq.heappop(ready)
        finish = now + remaining[current]
        next_release = pending[next_idx].release if next_idx < len(pending) else None

        if next_release is not None and next_release < finish:
            # Preempted by the next release: jump to that event.
            segments.append(Segment(current, now, next_release))
            remaining[current] -= next_release - now
            now = next_release
            task = by_id[current]
            heapq.heappush(ready, (task.deadline, task.release, task.task_id))
        else:
            # Run to completion: jump to the finish time.
            segments.append(Segment(current, now, finish))
            now = finish
            completions[current] = finish
            del remaining[current]
            task = by_id[current]
            if finish > task.deadline:
                missed.append(current)

        events += 1
        if event_budget is not None and events > event_budget:
            return ScheduleResult(
                status=Status.UNDECIDED,
                segments=segments,
                completions=completions,
                missed=missed,
                evidence=None,
                events_used=events,
            )

    if missed:
        return ScheduleResult(
            status=Status.INFEASIBLE,
            segments=segments,
            completions=completions,
            missed=sorted(missed),
            evidence=find_overload_window(task_list),
            events_used=events,
        )
    return ScheduleResult(
        status=Status.FEASIBLE,
        segments=segments,
        completions=completions,
        missed=[],
        evidence=None,
        events_used=events,
    )


def _demo() -> None:
    print("=== 1) Feasible (and optimal, since preemptive EDF is optimal) ===")
    tasks = [
        Task(1, release=0, work=3, deadline=7),
        Task(2, release=1, work=2, deadline=4),
        Task(3, release=2, work=Fraction(1, 2), deadline=6),
    ]
    result = simulate(tasks)
    print(f"status: {result.status.value} (optimal: {result.optimal})")
    for seg in result.segments:
        print(f"  task {seg.task_id} runs [{seg.start}, {seg.end})")
    for tid, done in sorted(result.completions.items()):
        print(f"  task {tid} completes at {done}")

    print("\n=== 2) Infeasible with overload-window evidence ===")
    tasks = [
        Task(1, release=0, work=2, deadline=3),
        Task(2, release=0, work=2, deadline=3),
        Task(3, release=0, work=1, deadline=5),
    ]
    result = simulate(tasks)
    print(f"status: {result.status.value}, missed: {result.missed}")
    print(f"evidence: {result.evidence}")

    print("\n=== 3) Undecided: event budget exhausted ===")
    tasks = [Task(i, release=0, work=1, deadline=10 + i) for i in range(1, 6)]
    result = simulate(tasks, event_budget=2)
    print(f"status: {result.status.value} after {result.events_used} events "
          f"(completed: {sorted(result.completions)})")


if __name__ == "__main__":
    _demo()
