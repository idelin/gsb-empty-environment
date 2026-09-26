"""Instance data model: exact integer/rational parsing and validation."""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Any


class InstanceError(ValueError):
    """Raised when an instance description is invalid."""


class OversizeJobError(InstanceError):
    """Raised when a single job exceeds the batch capacity (instance rejected)."""


def parse_number(value: Any) -> Fraction:
    """Parse an int, float, decimal string or 'p/q' string into a Fraction."""
    if isinstance(value, bool):
        raise InstanceError(f"invalid number: {value!r}")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        text = value.strip()
        try:
            return Fraction(text)
        except (ValueError, ZeroDivisionError) as exc:
            raise InstanceError(f"invalid number: {value!r}") from exc
    raise InstanceError(f"invalid number: {value!r}")


@dataclass(frozen=True)
class Job:
    jid: str
    family: str
    size: Fraction
    time: Fraction
    deadline: Fraction
    cost: Fraction


@dataclass(frozen=True)
class Instance:
    capacity: Fraction
    setup_cost: Fraction
    jobs: tuple[Job, ...]

    @property
    def job_cost_sum(self) -> Fraction:
        return sum((j.cost for j in self.jobs), Fraction(0))


def load_instance(data: dict) -> Instance:
    """Build and validate an Instance from a decoded JSON object."""
    if not isinstance(data, dict):
        raise InstanceError("instance must be a JSON object")
    for field in ("capacity", "setup_cost", "jobs"):
        if field not in data:
            raise InstanceError(f"missing field: {field!r}")
    capacity = parse_number(data["capacity"])
    setup_cost = parse_number(data["setup_cost"])
    raw_jobs = data["jobs"]
    if not isinstance(raw_jobs, list):
        raise InstanceError("'jobs' must be a list")

    jobs: list[Job] = []
    seen_ids: set[str] = set()
    for pos, raw in enumerate(raw_jobs, start=1):
        if not isinstance(raw, dict):
            raise InstanceError(f"job #{pos}: must be an object")
        for field in ("family", "size", "time", "deadline"):
            if field not in raw:
                raise InstanceError(f"job #{pos}: missing field {field!r}")
        job = Job(
            jid=str(raw.get("id", f"j{pos}")),
            family=str(raw["family"]),
            size=parse_number(raw["size"]),
            time=parse_number(raw["time"]),
            deadline=parse_number(raw["deadline"]),
            cost=parse_number(raw.get("cost", 0)),
        )
        if job.jid in seen_ids:
            raise InstanceError(f"duplicate job id: {job.jid!r}")
        seen_ids.add(job.jid)
        jobs.append(job)

    if capacity <= 0:
        raise InstanceError("capacity must be positive")
    if setup_cost < 0:
        raise InstanceError("setup_cost must be non-negative")
    for job in jobs:
        if job.size <= 0:
            raise InstanceError(f"job {job.jid!r}: size must be positive")
        if job.time < 0:
            raise InstanceError(f"job {job.jid!r}: time must be non-negative")
        if job.deadline < 0:
            raise InstanceError(f"job {job.jid!r}: deadline must be non-negative")
        if job.cost < 0:
            raise InstanceError(f"job {job.jid!r}: cost must be non-negative")

    oversize = [j.jid for j in jobs if j.size > capacity]
    if oversize:
        raise OversizeJobError(
            "job(s) exceed batch capacity and cannot be split: "
            + ", ".join(oversize)
        )
    return Instance(capacity=capacity, setup_cost=setup_cost, jobs=tuple(jobs))
