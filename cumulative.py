"""Cumulative scheduling with compulsory-interval (timetable) propagation.

Each task has a finite domain of integer start times, a duration and a
resource demand.  A single resource with fixed capacity must never be
exceeded.  Propagation only removes start values that are impossible in
*every* schedule (sound pruning); overloads are reported with a concrete
time-window evidence.  Claimed solutions are verified by an independent
checker against the original constraints.

Standard library only.  Requires Python 3.11+.
"""

from __future__ import annotations

from dataclasses import dataclass, replace


def domain(lo: int, hi: int) -> frozenset:
    """Inclusive integer start domain [lo, hi]."""
    if lo > hi:
        raise ValueError(f"empty domain [{lo}, {hi}]")
    return frozenset(range(lo, hi + 1))


@dataclass(frozen=True)
class Task:
    """A task: `starts` is its finite start-time domain, `duration` its
    processing time (>= 0) and `demand` its resource usage (>= 0) while
    running.  A zero-duration task never occupies the resource."""

    name: str
    starts: frozenset
    duration: int
    demand: int

    def __post_init__(self):
        if not self.name:
            raise ValueError("task name must be non-empty")
        if not self.starts:
            raise ValueError(f"task {self.name!r}: start domain must be non-empty")
        if not all(isinstance(s, int) for s in self.starts):
            raise ValueError(f"task {self.name!r}: start times must be integers")
        if self.duration < 0:
            raise ValueError(f"task {self.name!r}: negative duration")
        if self.demand < 0:
            raise ValueError(f"task {self.name!r}: negative demand")


def validate(tasks, capacity):
    """Static model checks.  A task whose demand alone exceeds the
    capacity makes the instance trivially infeasible: fail immediately.
    Zero-duration tasks never occupy the resource and are exempt."""
    if capacity < 0:
        raise ValueError("capacity must be non-negative")
    names = [t.name for t in tasks]
    if len(set(names)) != len(names):
        raise ValueError("task names must be unique")
    for t in tasks:
        if t.duration > 0 and t.demand > capacity:
            raise ValueError(
                f"task {t.name!r} requires {t.demand} > capacity {capacity}: "
                "instance trivially infeasible"
            )


@dataclass(frozen=True)
class OverloadEvidence:
    """Proof that no schedule exists: every task in `tasks` necessarily
    occupies the whole window [window_start, window_end), and their
    demands add up to `required` > `capacity`."""

    window_start: int
    window_end: int
    tasks: tuple
    required: int
    capacity: int

    def __str__(self):
        return (
            f"capacity exceeded on time window [{self.window_start}, "
            f"{self.window_end}): tasks {list(self.tasks)} necessarily occupy "
            f"the whole window, requiring {self.required} > capacity "
            f"{self.capacity}"
        )


class Overload(Exception):
    """Raised when compulsory usage provably exceeds the capacity."""

    def __init__(self, evidence: OverloadEvidence):
        super().__init__(str(evidence))
        self.evidence = evidence


def compulsory_interval(task):
    """Interval [lo, hi) the task occupies for *every* start in its
    domain, or None if there is no such interval (or duration is 0)."""
    if task.duration == 0:
        return None
    earliest = min(task.starts)
    latest = max(task.starts)
    lo, hi = latest, earliest + task.duration
    return (lo, hi) if lo < hi else None


def _usage_profile(tasks):
    """Map time -> total demand of compulsory intervals covering it."""
    usage = {}
    for t in tasks:
        comp = compulsory_interval(t)
        if comp is None or t.demand == 0:
            continue
        for time in range(comp[0], comp[1]):
            usage[time] = usage.get(time, 0) + t.demand
    return usage


def _check_overload(tasks, usage, capacity):
    overloaded = sorted(time for time, used in usage.items() if used > capacity)
    if not overloaded:
        return
    start = overloaded[0]
    end = start + 1
    while usage.get(end, 0) > capacity:
        end += 1
    covering = [
        t for t in tasks
        if t.demand > 0
        and (c := compulsory_interval(t)) is not None
        and c[0] <= start and c[1] >= end
    ]
    required = sum(t.demand for t in covering)
    if required <= capacity:
        # The overload comes from tasks covering single points only;
        # fall back to the first overloaded point as the evidence window.
        covering = [
            t for t in tasks
            if t.demand > 0
            and (c := compulsory_interval(t)) is not None
            and c[0] <= start < c[1]
        ]
        required = sum(t.demand for t in covering)
        end = start + 1
    raise Overload(
        OverloadEvidence(start, end, tuple(t.name for t in covering),
                         required, capacity)
    )


def propagate(tasks, capacity):
    """Timetable filtering to fixpoint.

    Returns a list of pruned domains (frozensets aligned with `tasks`),
    or None if a domain is wiped out (instance infeasible).  Raises
    Overload carrying time-window evidence when compulsory usage exceeds
    the capacity.  Only values infeasible in every schedule are removed.
    """
    domains = [set(t.starts) for t in tasks]
    while True:
        current = [replace(t, starts=frozenset(d)) for t, d in zip(tasks, domains)]
        usage = _usage_profile(current)
        _check_overload(current, usage, capacity)
        changed = False
        for i, task in enumerate(current):
            if task.duration == 0 or task.demand == 0:
                continue
            comp = compulsory_interval(task)
            kept = set()
            for s in domains[i]:
                feasible = True
                for time in range(s, s + task.duration):
                    others = usage.get(time, 0)
                    if comp is not None and comp[0] <= time < comp[1]:
                        others -= task.demand  # exclude own contribution
                    if others + task.demand > capacity:
                        feasible = False
                        break
                if feasible:
                    kept.add(s)
            if not kept:
                return None
            if kept != domains[i]:
                domains[i] = kept
                changed = True
        if not changed:
            return [frozenset(d) for d in domains]


def check_solution(tasks, capacity, assignment):
    """Independent checker: verifies a claimed solution against the
    original constraints -- every start inside its original domain and
    resource usage within capacity at all times.  Zero-duration tasks
    occupy nothing."""
    tasks_by_name = {t.name: t for t in tasks}
    if set(assignment) != set(tasks_by_name):
        return False
    events = []
    for name, start in assignment.items():
        task = tasks_by_name[name]
        if start not in task.starts:
            return False
        if task.duration == 0 or task.demand == 0:
            continue
        events.append((start, task.demand))
        events.append((start + task.duration, -task.demand))
    usage = 0
    # Ends (negative delta) sort before starts at the same time point.
    for _, delta in sorted(events):
        usage += delta
        if usage > capacity:
            return False
    return True


def _search(tasks, capacity):
    try:
        pruned = propagate(tasks, capacity)
    except Overload:
        return None
    if pruned is None:
        return None
    branch = None
    for i, d in enumerate(pruned):
        if len(d) > 1 and (branch is None or len(d) < len(pruned[branch])):
            branch = i
    if branch is None:
        return {t.name: next(iter(d)) for t, d in zip(tasks, pruned)}
    for s in sorted(pruned[branch]):
        narrowed = [
            replace(t, starts=frozenset({s}) if i == branch else d)
            for i, (t, d) in enumerate(zip(tasks, pruned))
        ]
        result = _search(narrowed, capacity)
        if result is not None:
            return result
    return None


def solve(tasks, capacity):
    """Solve a cumulative instance; returns {name: start} or None.

    Raises ValueError immediately if a single task's demand exceeds the
    capacity.  Any returned assignment is first verified by the
    independent checker against the original constraints.
    """
    tasks = list(tasks)
    validate(tasks, capacity)
    result = _search(tasks, capacity)
    if result is not None and not check_solution(tasks, capacity, result):
        raise RuntimeError("internal error: candidate failed independent check")
    return result


def _demo():
    print("=== Demo 1: solvable instance (capacity 3) ===")
    tasks = [
        Task("alpha", domain(0, 2), duration=4, demand=2),
        Task("beta", domain(0, 6), duration=3, demand=2),
        Task("gamma", domain(0, 6), duration=2, demand=1),
        Task("milestone", domain(0, 6), duration=0, demand=0),
    ]
    pruned = propagate(tasks, 3)
    for t, d in zip(tasks, pruned):
        print(f"  {t.name}: domain {sorted(t.starts)} -> {sorted(d)}")
    solution = solve(tasks, 3)
    print(f"  solution: {solution}")
    print(f"  independent checker: {check_solution(tasks, 3, solution)}")

    print("\n=== Demo 2: infeasible, overload with time-window evidence ===")
    tasks = [
        Task("x", domain(0, 1), duration=2, demand=1),
        Task("y", domain(0, 1), duration=2, demand=1),
    ]
    try:
        propagate(tasks, 1)
    except Overload as exc:
        print(f"  overload detected: {exc.evidence}")
    print(f"  solve returns: {solve(tasks, 1)}")

    print("\n=== Demo 3: propagation wipes out a domain (infeasible) ===")
    tasks = [
        Task("a", domain(0, 2), duration=2, demand=1),
        Task("b", frozenset({1}), duration=2, demand=1),
    ]
    print(f"  propagate returns: {propagate(tasks, 1)}")
    print(f"  solve returns: {solve(tasks, 1)}")

    print("\n=== Demo 4: single task demand exceeds capacity ===")
    try:
        solve([Task("big", domain(0, 3), duration=2, demand=5)], 3)
    except ValueError as exc:
        print(f"  immediate failure: {exc}")


if __name__ == "__main__":
    _demo()
