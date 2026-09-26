"""Single-machine scheduling with release times, deadlines and type setups.

Exact branch-and-bound minimization of total tardiness (no external solvers).

- Each job has a release time, a processing time, a deadline and a type.
- Consecutive jobs of the same type need no setup; switching types costs a
  type-dependent setup time.
- The machine may idle: waiting for a not-yet-released job can be better
  than starting an available job immediately, so idling decisions are part
  of the search (branching over *which* job runs next covers them).
- The result distinguishes OPTIMAL (search space exhausted, proven),
  FEASIBLE (complete schedule found, budget exhausted before proof) and
  UNDECIDED (budget exhausted before any complete schedule).  A
  budget-truncated run never receives the optimal label.

Standard library only.  All time arithmetic is exact: data may be given as
``int`` or ``fractions.Fraction`` (synthetic integer or rational data).
"""

from __future__ import annotations

import time as _time
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Callable, Hashable, Optional, Sequence, Union

Number = Union[int, Fraction]

__all__ = [
    "Job",
    "ScheduledJob",
    "Status",
    "SolveResult",
    "solve",
    "build_schedule",
    "lower_bound",
    "zero_setup",
    "constant_setup",
    "matrix_setup",
]


@dataclass(frozen=True)
class Job:
    """A job with release time, processing time, deadline and type."""

    job_id: Hashable
    release: Number
    processing: Number
    deadline: Number
    job_type: Hashable

    def __post_init__(self) -> None:
        if self.processing <= 0:
            raise ValueError("processing time must be positive")
        if self.release < 0:
            raise ValueError("release time must be non-negative")


# A setup function maps (previous type, next type) to a setup time.
# The previous type is None for the first job on the machine.
SetupFn = Callable[[Optional[Hashable], Hashable], Number]


def zero_setup() -> SetupFn:
    """No setup times at all."""
    return lambda prev, nxt: 0


def constant_setup(switch_time: Number) -> SetupFn:
    """Same type: 0; different type: ``switch_time``; first job: 0."""
    if switch_time < 0:
        raise ValueError("setup time must be non-negative")

    def setup(prev: Optional[Hashable], nxt: Hashable) -> Number:
        if prev is None or prev == nxt:
            return 0
        return switch_time

    return setup


def matrix_setup(matrix: dict, first_setup: Number = 0) -> SetupFn:
    """Type-pair setup matrix; same-type entries are forced to 0."""

    def setup(prev: Optional[Hashable], nxt: Hashable) -> Number:
        if prev is None:
            return first_setup
        if prev == nxt:
            return 0
        return matrix[prev][nxt]

    return setup


class Status(Enum):
    """Outcome of a solve call; the three cases are kept strictly apart."""

    OPTIMAL = "optimal"      # search exhausted within budget, proven optimal
    FEASIBLE = "feasible"    # complete schedule found, optimality unproven
    UNDECIDED = "undecided"  # budget exhausted before any complete schedule


@dataclass(frozen=True)
class ScheduledJob:
    job: Job
    start: Number
    completion: Number
    tardiness: Number


@dataclass(frozen=True)
class SolveResult:
    status: Status
    schedule: Optional[tuple]          # tuple[ScheduledJob, ...] or None
    total_tardiness: Optional[Number]  # None when UNDECIDED
    certified_lower_bound: Optional[Number]
    nodes_explored: int
    budget_exhausted: bool


def lower_bound(time_now: Number, jobs: Sequence[Job]) -> Number:
    """Valid lower bound on the *additional* tardiness of ``jobs``.

    Combines two relaxations (setups ignored in both, releases ignored in
    the second), so the result never exceeds the true optimum:

    A) each job completes no earlier than max(time_now, release) + p_j;
       sum the per-job minimal tardiness;
    B) the k-th completion is no earlier than time_now plus the k smallest
       processing times; pairing ascending completions with ascending
       deadlines minimizes total tardiness over all assignments
       (rearrangement inequality for the convex cost max(0, c - d)).
    """
    independent = 0
    for job in jobs:
        earliest = max(time_now, job.release) + job.processing
        if earliest > job.deadline:
            independent += earliest - job.deadline

    running = time_now
    cumulative = 0
    for proc, due in zip(
        sorted(job.processing for job in jobs),
        sorted(job.deadline for job in jobs),
    ):
        running += proc
        if running > due:
            cumulative += running - due

    return max(independent, cumulative)


def build_schedule(
    jobs: Sequence[Job], order: Sequence[int], setup: SetupFn
) -> list:
    """Realize a permutation of job indices as an earliest-start schedule."""
    schedule = []
    time_now = 0
    last_type: Optional[Hashable] = None
    for idx in order:
        job = jobs[idx]
        start = max(time_now + setup(last_type, job.job_type), job.release)
        completion = start + job.processing
        tardiness = completion - job.deadline
        if tardiness < 0:
            tardiness = 0
        schedule.append(ScheduledJob(job, start, completion, tardiness))
        time_now = completion
        last_type = job.job_type
    return schedule


class _BranchAndBound:
    def __init__(
        self,
        jobs: list,
        setup: SetupFn,
        node_budget: Optional[int],
        time_budget: Optional[float],
    ) -> None:
        self.jobs = jobs
        self.setup = setup
        self.node_budget = node_budget
        self.deadline_at = (
            None if time_budget is None else _time.monotonic() + time_budget
        )
        self.nodes = 0
        self.exhausted = False
        self.best_tardiness: Optional[Number] = None
        self.best_order: Optional[list] = None
        self.root_lower_bound: Number = 0

    def run(self) -> None:
        self.root_lower_bound = lower_bound(0, self.jobs)
        self._dfs(0, None, tuple(range(len(self.jobs))), 0, [])

    def _priority(self, time_now: Number, last_type: Optional[Hashable], idx: int):
        # Greedy key: least incremental tardiness first, then earliest
        # completion; the first DFS dive therefore yields a greedy schedule.
        job = self.jobs[idx]
        start = max(time_now + self.setup(last_type, job.job_type), job.release)
        completion = start + job.processing
        tardiness = completion - job.deadline
        if tardiness < 0:
            tardiness = 0
        return (tardiness, completion, idx)

    def _dfs(
        self,
        time_now: Number,
        last_type: Optional[Hashable],
        remaining: tuple,
        tardiness: Number,
        order: list,
    ) -> None:
        if self.exhausted:
            return
        self.nodes += 1
        if self.node_budget is not None and self.nodes > self.node_budget:
            self.exhausted = True
            return
        if self.deadline_at is not None and _time.monotonic() >= self.deadline_at:
            self.exhausted = True
            return

        if not remaining:
            if self.best_tardiness is None or tardiness < self.best_tardiness:
                self.best_tardiness = tardiness
                self.best_order = list(order)
            return

        if self.best_tardiness is not None:
            bound = tardiness + lower_bound(
                time_now, [self.jobs[i] for i in remaining]
            )
            if bound >= self.best_tardiness:
                return

        ordered = sorted(
            remaining, key=lambda i: self._priority(time_now, last_type, i)
        )
        for idx in ordered:
            job = self.jobs[idx]
            start = max(time_now + self.setup(last_type, job.job_type), job.release)
            completion = start + job.processing
            step = completion - job.deadline
            if step < 0:
                step = 0
            nxt = tuple(i for i in ordered if i != idx)
            order.append(idx)
            self._dfs(completion, job.job_type, nxt, tardiness + step, order)
            order.pop()
            if self.exhausted:
                return


def solve(
    jobs: Sequence[Job],
    setup: Optional[SetupFn] = None,
    *,
    node_budget: Optional[int] = None,
    time_budget: Optional[float] = None,
) -> SolveResult:
    """Minimize total tardiness by exact branch and bound.

    ``node_budget`` caps the number of search nodes, ``time_budget`` caps
    wall-clock seconds; either may be exceeded by at most the current node.
    When a budget stops the search, the best complete schedule found is
    returned with status FEASIBLE (never OPTIMAL); if none was completed
    in time, the status is UNDECIDED and the schedule is None.
    """
    job_list = list(jobs)
    if setup is None:
        setup = zero_setup()
    if node_budget is not None and node_budget < 0:
        raise ValueError("node_budget must be non-negative")
    if time_budget is not None and time_budget < 0:
        raise ValueError("time_budget must be non-negative")

    search = _BranchAndBound(job_list, setup, node_budget, time_budget)
    search.run()

    if search.best_order is None:
        schedule = None
        total = None
    else:
        schedule = tuple(build_schedule(job_list, search.best_order, setup))
        total = search.best_tardiness

    if not search.exhausted:
        status = Status.OPTIMAL
        certified = total
    elif schedule is not None:
        status = Status.FEASIBLE
        certified = search.root_lower_bound
    else:
        status = Status.UNDECIDED
        certified = search.root_lower_bound

    return SolveResult(
        status=status,
        schedule=schedule,
        total_tardiness=total,
        certified_lower_bound=certified,
        nodes_explored=search.nodes,
        budget_exhausted=search.exhausted,
    )
