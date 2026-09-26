"""Event-driven preemptive EDF scheduler for a single processor.

Model
-----
Each task has a release time, a work amount (execution requirement) and a
deadline.  All numbers are exact integers or rationals (``fractions.Fraction``);
no floats and no external solvers are involved.  The processor may run at most
one job at a time, jobs are preemptable, and a job may never run before its
release time.

The simulation is event driven: time jumps directly from one event (a release
or a completion) to the next, so the running time depends on the number of
tasks, not on the magnitude of the numbers.  At most ``2 * n`` events occur
for ``n`` tasks.

Outcomes (kept strictly separate)
---------------------------------
* ``Status.OPTIMAL``   - EDF met every deadline.  Because preemptive EDF is
  optimal on a uniprocessor (Horn, 1974), a feasible EDF schedule is also an
  optimal one, so feasibility and optimality coincide and are reported as
  ``OPTIMAL`` (``result.feasible`` is True).
* ``Status.INFEASIBLE`` - a deadline was missed.  Since EDF is optimal, no
  feasible schedule exists.  The result carries an ``Evidence`` object: a time
  window ``[t0, d]`` whose total demand (work released at or after ``t0`` with
  deadline at most ``d``) strictly exceeds the supply ``d - t0``.  This is a
  machine-checkable certificate of infeasibility (see ``verify_evidence``).
* ``Status.UNDECIDED`` - the event budget (``max_events``) was exhausted before
  the simulation finished.  Nothing is claimed about feasibility.

Tie-breaking: jobs that arrive at the same time and share a deadline are
ordered by task id (stable).  Zero-work tasks complete immediately at their
release time.
"""

from bisect import bisect_left
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from heapq import heappop, heappush

DEFAULT_EVENT_BUDGET = 1_000_000


class Status(Enum):
    OPTIMAL = "optimal"        # feasible, and optimal by EDF optimality
    INFEASIBLE = "infeasible"  # deadline miss + certificate window
    UNDECIDED = "undecided"    # event budget exhausted


@dataclass(frozen=True)
class Task:
    task_id: int
    release: Fraction
    work: Fraction
    deadline: Fraction

    def __post_init__(self):
        object.__setattr__(self, "release", Fraction(self.release))
        object.__setattr__(self, "work", Fraction(self.work))
        object.__setattr__(self, "deadline", Fraction(self.deadline))
        if self.work < 0:
            raise ValueError(f"task {self.task_id}: negative work {self.work}")
        if self.deadline < self.release:
            raise ValueError(
                f"task {self.task_id}: deadline {self.deadline} "
                f"before release {self.release}"
            )


@dataclass
class Segment:
    """One contiguous execution interval; task_id is None for idle time."""

    start: Fraction
    end: Fraction
    task_id: object  # int or None


@dataclass(frozen=True)
class Evidence:
    """Certificate of infeasibility: demand > supply in the window."""

    window_start: Fraction
    window_end: Fraction
    demand: Fraction
    supply: Fraction
    missed_task_id: int
    missed_at: Fraction


@dataclass
class Result:
    status: Status
    segments: list
    completions: dict
    evidence: object  # Evidence or None
    events_processed: int

    @property
    def feasible(self):
        return self.status is Status.OPTIMAL

    @property
    def optimal(self):
        return self.status is Status.OPTIMAL


def _append_segment(segments, start, end, task_id):
    if end <= start:
        return
    last = segments[-1] if segments else None
    if last is not None and last.task_id == task_id and last.end == start:
        last.end = end
    else:
        segments.append(Segment(start, end, task_id))


def _build_evidence(segments, by_id, missed_id, missed_at):
    """Walk back from the missed deadline d over the recorded timeline.

    t0 is the earliest time such that throughout [t0, d] the processor was
    busy executing jobs with deadline <= d.  Under EDF every such job was
    released at or after t0 (otherwise it would have been running just before
    t0 instead of an idle period or a later-deadline job), so the demand in
    the window is exactly the work of tasks with release >= t0 and
    deadline <= d, and it must exceed d - t0 because the missed job still has
    remaining work at time d.
    """
    d = by_id[missed_id].deadline
    starts = [seg.start for seg in segments]
    i = bisect_left(starts, d) - 1  # rightmost segment starting before d
    t0 = d
    while i >= 0:
        seg = segments[i]
        if seg.end < t0:
            break  # gap: not contiguous with the window
        if seg.task_id is None:
            break  # processor idle just before t0
        if by_id[seg.task_id].deadline > d:
            break  # a later-deadline job was running just before t0
        t0 = seg.start
        i -= 1
    demand = sum(
        (t.work for t in by_id.values() if t.release >= t0 and t.deadline <= d),
        Fraction(0),
    )
    return Evidence(
        window_start=t0,
        window_end=d,
        demand=demand,
        supply=d - t0,
        missed_task_id=missed_id,
        missed_at=missed_at,
    )


def verify_evidence(tasks, evidence):
    """Independently re-check an infeasibility certificate."""
    by_id = {t.task_id: t for t in tasks}
    t0, d = evidence.window_start, evidence.window_end
    demand = sum(
        (t.work for t in by_id.values() if t.release >= t0 and t.deadline <= d),
        Fraction(0),
    )
    supply = d - t0
    return (
        supply >= 0
        and demand == evidence.demand
        and supply == evidence.supply
        and demand > supply
    )


def simulate(tasks, max_events=DEFAULT_EVENT_BUDGET):
    """Run preemptive EDF and return a Result.

    ``tasks`` is an iterable of Task objects (or (id, release, work, deadline)
    tuples).  ``max_events`` bounds the number of processed events; exceeding
    it yields Status.UNDECIDED instead of an answer.
    """
    task_list = [t if isinstance(t, Task) else Task(*t) for t in tasks]
    by_id = {}
    for t in task_list:
        if t.task_id in by_id:
            raise ValueError(f"duplicate task id {t.task_id}")
        by_id[t.task_id] = t

    releases = sorted(task_list, key=lambda t: (t.release, t.task_id))
    ready = []  # heap of (deadline, task_id); stable by task_id on ties
    remaining = {t.task_id: t.work for t in task_list}
    segments = []
    completions = {}
    events = 0
    idx = 0
    n = len(releases)
    now = releases[0].release if releases else Fraction(0)

    def undecided():
        return Result(Status.UNDECIDED, segments, completions, None, events)

    def infeasible(missed_id):
        evidence = _build_evidence(segments, by_id, missed_id, now)
        return Result(Status.INFEASIBLE, segments, completions, evidence, events)

    while idx < n or ready:
        # Absorb every release at the current time (stable id order).
        while idx < n and releases[idx].release <= now:
            task = releases[idx]
            idx += 1
            events += 1
            if task.work == 0:
                completions[task.task_id] = task.release  # completes immediately
            else:
                heappush(ready, (task.deadline, task.task_id))
            if events > max_events:
                return undecided()

        if not ready:
            if idx >= n:
                break  # only zero-work tasks were left; nothing to run
            # Idle: jump straight to the next release (event jumping).
            nxt = releases[idx].release
            _append_segment(segments, now, nxt, None)
            now = nxt
            continue

        deadline, jid = ready[0]
        if deadline <= now:
            # A ready job with remaining work cannot finish by its deadline.
            return infeasible(jid)

        completion = now + remaining[jid]
        nxt = releases[idx].release if idx < n else None
        if nxt is not None and nxt < completion:
            # Run until the next release (which may preempt).
            _append_segment(segments, now, nxt, jid)
            remaining[jid] -= nxt - now
            now = nxt
        else:
            # Run until the current job completes.
            _append_segment(segments, now, completion, jid)
            remaining[jid] = Fraction(0)
            heappop(ready)
            completions[jid] = completion
            now = completion
            events += 1
            if completion > deadline:
                return infeasible(jid)
            if events > max_events:
                return undecided()

    return Result(Status.OPTIMAL, segments, completions, None, events)


def _fmt(x):
    return str(x.numerator) if x.denominator == 1 else f"{x.numerator}/{x.denominator}"


def _print_result(name, tasks, result):
    print(f"=== {name} ===")
    print(f"status    : {result.status.value} (feasible={result.feasible})")
    print(f"events    : {result.events_processed}")
    if result.segments:
        print("schedule  :")
        for seg in result.segments:
            label = "idle" if seg.task_id is None else f"task {seg.task_id}"
            print(f"  [{_fmt(seg.start)}, {_fmt(seg.end)})  {label}")
    if result.completions:
        done = ", ".join(
            f"task {tid} @ {_fmt(t)}" for tid, t in sorted(result.completions.items())
        )
        print(f"completed : {done}")
    if result.evidence is not None:
        ev = result.evidence
        print(
            f"evidence  : task {ev.missed_task_id} missed its deadline at "
            f"{_fmt(ev.missed_at)}; window [{_fmt(ev.window_start)}, "
            f"{_fmt(ev.window_end)}] has demand {_fmt(ev.demand)} > supply "
            f"{_fmt(ev.supply)}"
        )
        print(f"verified  : {verify_evidence(tasks, ev)}")
    print()


def main():
    feasible_tasks = [
        Task(1, 0, 2, 5),   # released at 0, needs 2 units, deadline 5
        Task(2, 1, 1, 3),   # preempts task 1 at time 1 (earlier deadline)
        Task(3, 2, 0, 4),   # zero work: completes immediately at release
        Task(4, 8, 1, 10),  # arrives after an idle gap
    ]
    _print_result(
        "feasible set -> OPTIMAL", feasible_tasks, simulate(feasible_tasks)
    )

    infeasible_tasks = [
        Task(1, 0, 3, 4),
        Task(2, 0, 3, 4),  # 6 units of work due by time 4: impossible
    ]
    _print_result(
        "infeasible set -> INFEASIBLE with evidence",
        infeasible_tasks,
        simulate(infeasible_tasks),
    )

    budget_tasks = [Task(i, 0, 1, 10) for i in range(5)]
    _print_result(
        "tiny event budget -> UNDECIDED",
        budget_tasks,
        simulate(budget_tasks, max_events=2),
    )


if __name__ == "__main__":
    main()
