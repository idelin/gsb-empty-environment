from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass, replace
from typing import TypeAlias

from .model import Duration, Instant, WallClock


TableVersion: TypeAlias = int | str


@dataclass(frozen=True, slots=True)
class Transition:
    at: Instant
    offset_after: Duration
    transition_id: str


@dataclass(frozen=True, slots=True)
class OffsetSegment:
    start: Instant | None
    end: Instant | None
    offset: Duration
    transition_id: str | None

    def wall_start(self) -> WallClock | None:
        if self.start is None:
            return None
        return wall_at(self.start, self.offset)

    def wall_end(self) -> WallClock | None:
        if self.end is None:
            return None
        return wall_at(self.end, self.offset)


@dataclass(frozen=True, slots=True)
class TransitionTable:
    initial_offset: Duration
    transitions: tuple[Transition, ...]
    version: TableVersion = 1

    def __post_init__(self) -> None:
        if not isinstance(self.initial_offset, Duration):
            raise TypeError("initial_offset must be a Duration")
        transitions = tuple(self.transitions)
        seen: set[str] = set()
        for transition in transitions:
            if not isinstance(transition, Transition):
                raise TypeError("transitions must contain Transition values")
            if transition.transition_id in seen:
                raise ValueError(f"duplicate transition id: {transition.transition_id}")
            seen.add(transition.transition_id)
        if any(left.at >= right.at for left, right in zip(transitions, transitions[1:])):
            raise ValueError("transitions must be strictly ordered by instant")
        object.__setattr__(self, "transitions", transitions)

    def with_version(self, version: TableVersion) -> TransitionTable:
        return replace(self, version=version)

    def transition_times(self) -> tuple[Instant, ...]:
        return tuple(transition.at for transition in self.transitions)

    def offset_at(self, instant: Instant) -> Duration:
        index = bisect_right(self.transition_times(), instant)
        if index == 0:
            return self.initial_offset
        return self.transitions[index - 1].offset_after

    def transition_id_at(self, instant: Instant) -> str | None:
        index = bisect_right(self.transition_times(), instant)
        if index == 0:
            return None
        return self.transitions[index - 1].transition_id

    def segments(self) -> list[OffsetSegment]:
        result: list[OffsetSegment] = []
        start = None
        offset = self.initial_offset
        transition_id = None
        for transition in self.transitions:
            result.append(OffsetSegment(start, transition.at, offset, transition_id))
            start = transition.at
            offset = transition.offset_after
            transition_id = transition.transition_id
        result.append(OffsetSegment(start, None, offset, transition_id))
        return result


def wall_at(instant: Instant, offset: Duration) -> WallClock:
    return Duration(instant.unix_nanoseconds + offset.nanoseconds).to_wall()


def instant_at(wall: WallClock, offset: Duration) -> Instant:
    return Instant(wall.naive_nanoseconds - offset.nanoseconds)
