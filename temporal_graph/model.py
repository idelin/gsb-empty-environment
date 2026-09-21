from __future__ import annotations

from dataclasses import dataclass
from typing import Tuple


def _is_int(value: object) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


@dataclass(frozen=True)
class HalfOpenInterval:
    start: int
    end: int

    def __post_init__(self) -> None:
        if not _is_int(self.start) or not _is_int(self.end):
            raise TypeError("interval endpoints must be integers")
        if self.start > self.end:
            raise ValueError("interval start must be <= end")

    def __bool__(self) -> bool:
        return self.start < self.end

    def contains(self, time: int) -> bool:
        return self.start <= time < self.end

    def intersect(self, other: "HalfOpenInterval") -> "HalfOpenInterval | None":
        start = max(self.start, other.start)
        end = min(self.end, other.end)
        if start < end:
            return HalfOpenInterval(start, end)
        return None

    @property
    def length(self) -> int:
        return max(0, self.end - self.start)


@dataclass(frozen=True)
class EdgeRecord:
    edge_id: str
    source: str
    target: str
    window: HalfOpenInterval
    duration: int
    version: int = 1
    active: bool = True

    def __post_init__(self) -> None:
        if not isinstance(self.edge_id, str) or not self.edge_id:
            raise ValueError("edge id must be a non-empty string")
        if not isinstance(self.source, str) or not self.source:
            raise ValueError("vertex id must be a non-empty string")
        if not isinstance(self.target, str) or not self.target:
            raise ValueError("vertex id must be a non-empty string")
        if not _is_int(self.duration) or self.duration < 0:
            raise ValueError("duration must be a non-negative integer")
        if not _is_int(self.version) or self.version < 1:
            raise ValueError("version must be a positive integer")

    def valid_at(self, departure: int) -> bool:
        return self.active and self.window.contains(departure)


@dataclass(frozen=True)
class VertexRecord:
    vertex_id: str
    blocked: Tuple[HalfOpenInterval, ...] = ()
    version: int = 1

    def __post_init__(self) -> None:
        if not isinstance(self.vertex_id, str) or not self.vertex_id:
            raise ValueError("vertex id must be a non-empty string")
        merged = tuple(_merge_intervals(self.blocked))
        if merged != self.blocked:
            object.__setattr__(self, "blocked", merged)

    def blocked_at(self, time: int) -> bool:
        return any(window.contains(time) for window in self.blocked)

    def open_through(self, start: int, end: int) -> bool:
        if start > end:
            return False
        return all(not self.blocked_at(time) for time in range(start, end))

    def first_blocked_at_or_after(self, time: int) -> int | None:
        candidates = [
            window.start
            for window in self.blocked
            if window.end > time
        ]
        return min(candidates) if candidates else None


def _merge_intervals(
    intervals: tuple[HalfOpenInterval, ...]
) -> list[HalfOpenInterval]:
    ordered = sorted(intervals, key=lambda window: (window.start, window.end))
    merged: list[HalfOpenInterval] = []
    for window in ordered:
        if not merged or window.start > merged[-1].end:
            merged.append(window)
        elif window.end > merged[-1].end:
            merged[-1] = HalfOpenInterval(merged[-1].start, window.end)
    return merged


@dataclass(frozen=True)
class ConcurrentPath:
    steps: Tuple[EdgeRecord, ...]
    common_window: HalfOpenInterval

    @property
    def vertices(self) -> Tuple[str, ...]:
        return tuple([self.steps[0].source] + [step.target for step in self.steps])


@dataclass(frozen=True)
class JourneyStep:
    edge: EdgeRecord
    departure: int
    arrival: int
    wait_before: int

    @property
    def duration(self) -> int:
        return self.arrival - self.departure


@dataclass(frozen=True)
class Journey:
    source: str
    target: str
    departure: int
    arrival: int
    steps: Tuple[JourneyStep, ...]

    @property
    def found(self) -> bool:
        return self.target == self.source or bool(self.steps)
