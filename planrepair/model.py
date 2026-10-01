"""Data model for the plan-repair problem.

All time and cost data are exact rational numbers.  Inputs may be given as
``int``, ``fractions.Fraction`` or strings understood by ``Fraction``
(e.g. ``"3/2"``); everything is normalised to ``Fraction`` internally so the
search never deals with floating point error.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Mapping, Optional, Tuple, Union

Number = Union[int, Fraction, str]


def Q(value: Number) -> Fraction:
    """Coerce an int / Fraction / rational string to an exact ``Fraction``."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid numeric value")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"unsupported numeric value: {value!r}")


@dataclass(frozen=True)
class TimeWindow:
    """Half-open availability window ``[start, end)`` of a resource."""

    start: Fraction
    end: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "start", Q(self.start))
        object.__setattr__(self, "end", Q(self.end))
        if not self.start < self.end:
            raise ValueError(f"window start must be < end, got [{self.start}, {self.end})")


@dataclass(frozen=True)
class Resource:
    """A resource with a capacity and one or more availability windows."""

    rid: str
    windows: Tuple[TimeWindow, ...]
    capacity: int = 1

    def __post_init__(self) -> None:
        object.__setattr__(self, "windows", tuple(self.windows))
        if not self.windows:
            raise ValueError(f"resource {self.rid!r} needs at least one window")
        if isinstance(self.capacity, bool) or not isinstance(self.capacity, int) or self.capacity < 1:
            raise ValueError(f"resource {self.rid!r} capacity must be a positive int")


@dataclass(frozen=True)
class Task:
    """A task to be scheduled.

    ``costs`` maps eligible resource ids to the (non-negative) cost of
    running the task on that resource; resources absent from the mapping are
    not eligible.
    """

    tid: str
    duration: Fraction
    costs: Mapping[str, Fraction]
    release: Fraction = 0
    deadline: Optional[Fraction] = None

    def __post_init__(self) -> None:
        duration = Q(self.duration)
        if duration <= 0:
            raise ValueError(f"task {self.tid!r} duration must be positive")
        object.__setattr__(self, "duration", duration)
        costs = {rid: Q(c) for rid, c in dict(self.costs).items()}
        if not costs:
            raise ValueError(f"task {self.tid!r} needs at least one eligible resource")
        for rid, c in costs.items():
            if c < 0:
                raise ValueError(f"task {self.tid!r} has negative cost on {rid!r}")
        object.__setattr__(self, "costs", costs)
        object.__setattr__(self, "release", Q(self.release))
        if self.deadline is not None:
            object.__setattr__(self, "deadline", Q(self.deadline))


@dataclass(frozen=True)
class Placement:
    """Where a task is placed: resource id + start time."""

    rid: str
    start: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "start", Q(self.start))


@dataclass(frozen=True)
class Plan:
    """A plan: task placements plus the set of already-executed tasks.

    ``executed`` tasks are the frozen prefix: they already ran and can never
    be moved (in particular never moved into the future to be re-run).
    """

    assignments: Mapping[str, Placement]
    executed: frozenset = frozenset()

    def __post_init__(self) -> None:
        object.__setattr__(self, "assignments", dict(self.assignments))
        executed = frozenset(self.executed)
        unknown = executed - set(self.assignments)
        if unknown:
            raise ValueError(f"executed tasks missing from assignments: {sorted(unknown)}")
        object.__setattr__(self, "executed", executed)


@dataclass(frozen=True)
class Disruption:
    """What happened to the original plan: new tasks and/or failed resources."""

    added_tasks: Tuple[Task, ...] = ()
    failed_resources: frozenset = frozenset()

    def __post_init__(self) -> None:
        object.__setattr__(self, "added_tasks", tuple(self.added_tasks))
        object.__setattr__(self, "failed_resources", frozenset(self.failed_resources))
