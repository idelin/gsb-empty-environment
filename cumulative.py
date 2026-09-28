"""Cumulative scheduling: compulsory-part (timetable) propagation + solver.

Pure Python 3.11, standard library only. No SAT/SMT solver is used:
propagation only removes start values that are provably impossible,
and any claimed solution is verified by an independent checker against
the original constraints.

Model
-----
Each task has a known duration, a known resource demand, and a finite
domain of possible integer start times. A resource has a known capacity.
A task starting at ``s`` occupies ``demand`` units of the resource during
the half-open interval ``[s, s + duration)``. A zero-duration task
occupies nothing (its demand is irrelevant).
"""

from __future__ import annotations

from dataclasses import dataclass


# ---------------------------------------------------------------------------
# Data model
# ---------------------------------------------------------------------------

@dataclass
class Task:
    """A single activity to schedule.

    name:     unique identifier
    duration: non-negative integer length (0 means no resource usage)
    demand:   non-negative integer resource usage while running
    domain:   finite set of feasible integer start times
    """

    name: str
    duration: int
    demand: int
    domain: set


@dataclass(frozen=True)
class OverloadEvidence:
    """Time-window certificate that capacity is exceeded.

    During the whole half-open window [start, end), the compulsory parts
    of ``contributors`` alone already require ``demand`` > ``capacity``.
    """

    window: tuple  # (start, end)
    demand: int
    capacity: int
    contributors: tuple

    def __str__(self) -> str:
        return (
            f"overload in window [{self.window[0]}, {self.window[1]}): "
            f"compulsory demand {self.demand} > capacity {self.capacity}, "
            f"caused by {list(self.contributors)}"
        )


class InfeasibleError(Exception):
    """Base class for proven infeasibility."""


class OverloadError(InfeasibleError):
    """Compulsory parts (or one task alone) provably exceed capacity."""

    def __init__(self, evidence):
        self.evidence = evidence
        super().__init__("; ".join(str(e) for e in evidence))


class DomainWipeoutError(InfeasibleError):
    """Filtering removed every start value of a task.

    ``reasons`` maps each removed start time to ``(time, others_demand)``:
    placing the task at that start would exceed capacity at ``time``,
    where the other tasks' compulsory parts already use ``others_demand``.
    """

    def __init__(self, task_name, reasons):
        self.task_name = task_name
        self.reasons = reasons
        detail = ", ".join(
            f"start {s} blocked at t={t} (others use {u})"
            for s, (t, u) in sorted(reasons.items())
        )
        super().__init__(f"domain of {task_name!r} wiped out: {detail}")


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

def _validate(tasks, capacity):
    if not isinstance(capacity, int) or capacity < 0:
        raise ValueError("capacity must be a non-negative integer")
    names = set()
    for task in tasks:
        if task.name in names:
            raise ValueError(f"duplicate task name {task.name!r}")
        names.add(task.name)
        if task.duration < 0:
            raise ValueError(f"{task.name!r}: negative duration")
        if task.demand < 0:
            raise ValueError(f"{task.name!r}: negative demand")
        if not task.domain:
            raise ValueError(f"{task.name!r}: empty start domain")
        if not all(isinstance(s, int) for s in task.domain):
            raise ValueError(f"{task.name!r}: non-integer start in domain")
        # Immediate failure: a task that actually runs can never fit.
        # Zero-duration tasks occupy nothing, so their demand is ignored.
        if task.duration > 0 and task.demand > capacity:
            est = min(task.domain)
            evidence = OverloadEvidence(
                window=(est, est + task.duration),
                demand=task.demand,
                capacity=capacity,
                contributors=(task.name,),
            )
            raise OverloadError([evidence])


# ---------------------------------------------------------------------------
# Compulsory parts and overload evidence
# ---------------------------------------------------------------------------

def _compulsory_part(task):
    """Interval [lst, est + duration) the task occupies in every schedule.

    Returns None when the task has no compulsory part (including the
    zero-duration case, which never occupies the resource).
    """
    if task.duration == 0:
        return None
    est = min(task.domain)
    lst = max(task.domain)
    begin, end = lst, est + task.duration
    if begin < end:
        return (begin, end)
    return None


def _overload_evidence(tasks, capacity):
    """All maximal time windows where compulsory parts exceed capacity."""
    parts = {}
    for task in tasks:
        part = _compulsory_part(task)
        if part is not None:
            parts[task.name] = part

    usage = {}
    for name, (begin, end) in parts.items():
        demand = next(t.demand for t in tasks if t.name == name)
        for time in range(begin, end):
            usage[time] = usage.get(time, 0) + demand

    bad = sorted(time for time, used in usage.items() if used > capacity)
    evidence = []
    index = 0
    while index < len(bad):
        start = bad[index]
        contributors = tuple(sorted(
            name for name, (begin, end) in parts.items()
            if begin <= start < end
        ))
        demand = usage[start]
        end = start + 1
        index += 1
        # Extend while consecutive overloaded points share the same cause.
        while index < len(bad) and bad[index] == end:
            same = tuple(sorted(
                name for name, (b, e) in parts.items() if b <= end < e
            )) == contributors
            if not same:
                break
            end += 1
            index += 1
        evidence.append(OverloadEvidence(
            window=(start, end),
            demand=demand,
            capacity=capacity,
            contributors=contributors,
        ))
    return evidence


# ---------------------------------------------------------------------------
# Timetable (compulsory-part) propagation
# ---------------------------------------------------------------------------

def propagate(tasks, capacity):
    """Prune start domains with timetable filtering.

    A start value ``s`` of task ``i`` is removed only if placing task ``i``
    at ``s`` together with the *compulsory* usage of the other tasks
    exceeds ``capacity`` at some time point. Such a value cannot appear in
    any feasible schedule, so propagation never deletes a legal value.

    Returns a dict ``{task_name: pruned_domain}``. Input tasks are not
    mutated. Raises OverloadError / DomainWipeoutError on proven
    infeasibility.
    """
    _validate(tasks, capacity)
    domains = {task.name: set(task.domain) for task in tasks}

    changed = True
    while changed:
        changed = False
        current = [
            Task(t.name, t.duration, t.demand, domains[t.name]) for t in tasks
        ]
        evidence = _overload_evidence(current, capacity)
        if evidence:
            raise OverloadError(evidence)

        # Compulsory-part usage profile of all tasks.
        profile = {}
        for task in current:
            part = _compulsory_part(task)
            if part is None:
                continue
            for time in range(part[0], part[1]):
                profile[time] = profile.get(time, 0) + task.demand

        for task in current:
            if task.duration == 0 or task.demand == 0:
                continue
            # Usage of the *other* tasks only.
            others = dict(profile)
            part = _compulsory_part(task)
            if part is not None:
                for time in range(part[0], part[1]):
                    others[time] -= task.demand

            reasons = {}
            for start in domains[task.name]:
                for time in range(start, start + task.duration):
                    if others.get(time, 0) + task.demand > capacity:
                        reasons[start] = (time, others.get(time, 0))
                        break
            if reasons:
                remaining = domains[task.name] - reasons.keys()
                if not remaining:
                    raise DomainWipeoutError(task.name, reasons)
                domains[task.name] = remaining
                changed = True
    return domains


# ---------------------------------------------------------------------------
# Independent checker
# ---------------------------------------------------------------------------

def check(tasks, capacity, assignment):
    """Independently verify a schedule against the original constraints.

    Checks that every task is assigned a start from its *original* domain
    and that total demand never exceeds capacity at any time. Returns
    True/False; raises nothing for ordinary violations.
    """
    try:
        _validate(tasks, capacity)
    except (ValueError, OverloadError):
        return False
    if set(assignment) != {t.name for t in tasks}:
        return False
    usage = {}
    for task in tasks:
        start = assignment[task.name]
        if start not in task.domain:
            return False
        for time in range(start, start + task.duration):
            usage[time] = usage.get(time, 0) + task.demand
            if usage[time] > capacity:
                return False
    return True


# ---------------------------------------------------------------------------
# Solver (depth-first search + propagation, no SAT/SMT)
# ---------------------------------------------------------------------------

def _search(tasks, domains, capacity):
    open_tasks = [t for t in tasks if len(domains[t.name]) > 1]
    if not open_tasks:
        candidate = {t.name: next(iter(domains[t.name])) for t in tasks}
        # Propagation already proved consistency; the checker re-verifies.
        return candidate if check(tasks, capacity, candidate) else None

    branch = min(open_tasks, key=lambda t: len(domains[t.name]))
    for start in sorted(domains[branch.name]):
        sub_tasks = [
            Task(
                t.name,
                t.duration,
                t.demand,
                {start} if t.name == branch.name else set(domains[t.name]),
            )
            for t in tasks
        ]
        try:
            sub_domains = propagate(sub_tasks, capacity)
        except InfeasibleError:
            continue
        result = _search(sub_tasks, sub_domains, capacity)
        if result is not None:
            return result
    return None


def solve(tasks, capacity):
    """Find a feasible schedule, or return None if none exists.

    Any returned assignment is verified by the independent checker
    against the original task domains and capacity before being handed
    back; a mismatch raises RuntimeError (indicating an internal bug).
    """
    try:
        domains = propagate(tasks, capacity)
    except InfeasibleError:
        return None
    assignment = _search(tasks, domains, capacity)
    if assignment is None:
        return None
    if not check(tasks, capacity, assignment):
        raise RuntimeError("solver produced a schedule the checker rejects")
    return assignment
