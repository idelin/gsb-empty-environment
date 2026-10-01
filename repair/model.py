"""Data model and JSON parsing for the plan-repair tool.

All numeric data is synthetic and exact: integers or rationals.  Every
number is stored as ``fractions.Fraction`` so computations never lose
precision.  JSON inputs may use ints, floats (converted through their
decimal string) or strings such as "3/2", "1.5" or "7".
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Any, Dict, Optional, Tuple


def parse_number(value: Any) -> Fraction:
    """Parse an int / float / str into an exact Fraction."""
    if isinstance(value, bool):
        raise ValueError(f"not a number: {value!r}")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        try:
            return Fraction(value)
        except (ValueError, ZeroDivisionError) as exc:
            raise ValueError(f"not a number: {value!r}") from exc
    raise ValueError(f"not a number: {value!r}")


def fmt_number(value: Optional[Fraction]) -> Optional[str]:
    """Render a Fraction as a compact exact string ("9", "3/2")."""
    if value is None:
        return None
    return str(value)


@dataclass(frozen=True)
class Task:
    id: str
    machine: str
    duration: Fraction
    start: Optional[Fraction]  # None for new (not yet scheduled) tasks
    executed: bool = False

    @property
    def is_new(self) -> bool:
        return self.start is None

    @property
    def end(self) -> Optional[Fraction]:
        if self.start is None:
            return None
        return self.start + self.duration


@dataclass(frozen=True)
class Failure:
    """A resource-failure interval on one machine.

    ``end=None`` means a permanent shutdown: the machine is unavailable
    from ``start`` onwards (to infinity).
    """
    machine: str
    start: Fraction
    end: Optional[Fraction]


@dataclass(frozen=True)
class Scenario:
    now: Fraction
    tasks: Tuple[Task, ...]        # existing plan: executed prefix + future
    new_tasks: Tuple[Task, ...]    # added tasks that need placement
    failures: Tuple[Failure, ...]
    subset_budget: int
    placement_budget: int


DEFAULT_SUBSET_BUDGET = 100_000
DEFAULT_PLACEMENT_BUDGET = 100_000


def _parse_task(data: Dict[str, Any], is_new: bool) -> Task:
    try:
        task_id = data["id"]
        machine = data["machine"]
        duration = parse_number(data["duration"])
    except KeyError as exc:
        raise ValueError(f"task missing field {exc}") from exc
    if not isinstance(task_id, str) or not task_id:
        raise ValueError(f"task id must be a non-empty string: {task_id!r}")
    if not isinstance(machine, str) or not machine:
        raise ValueError(f"task {task_id!r}: machine must be a non-empty string")
    if is_new:
        start = None
        executed = False
    else:
        if "start" not in data:
            raise ValueError(f"existing task {task_id!r} missing 'start'")
        start = parse_number(data["start"])
        executed = bool(data.get("executed", False))
    return Task(id=task_id, machine=machine, duration=duration,
                start=start, executed=executed)


def _parse_failure(data: Dict[str, Any]) -> Failure:
    try:
        machine = data["machine"]
        start = parse_number(data["start"])
    except KeyError as exc:
        raise ValueError(f"failure missing field {exc}") from exc
    end = data.get("end")
    return Failure(machine=machine, start=start,
                   end=None if end is None else parse_number(end))


def parse_scenario(data: Dict[str, Any]) -> Scenario:
    """Build a Scenario from a decoded JSON object."""
    if not isinstance(data, dict):
        raise ValueError("scenario must be a JSON object")
    now = parse_number(data.get("now", 0))
    tasks = tuple(_parse_task(t, is_new=False) for t in data.get("tasks", []))
    new_tasks = tuple(_parse_task(t, is_new=True) for t in data.get("new_tasks", []))
    failures = tuple(_parse_failure(f) for f in data.get("failures", []))
    budgets = data.get("budget", {})
    subset_budget = int(budgets.get("subset", DEFAULT_SUBSET_BUDGET))
    placement_budget = int(budgets.get("placement", DEFAULT_PLACEMENT_BUDGET))
    if subset_budget < 0 or placement_budget < 0:
        raise ValueError("budgets must be non-negative")
    return Scenario(now=now, tasks=tasks, new_tasks=new_tasks,
                    failures=failures, subset_budget=subset_budget,
                    placement_budget=placement_budget)
