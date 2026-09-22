from __future__ import annotations

from bisect import bisect_left, bisect_right
from dataclasses import dataclass
from decimal import Decimal
from enum import Enum
from fractions import Fraction
from typing import Iterable, Optional, Sequence

from .arithmetic import (
    ExactTime,
    grid_time,
    interpolate_linear,
    round_output,
    to_fraction,
)


class Mode(str, Enum):
    LINEAR = "linear"
    STEP = "step"


class UnknownReason(str, Enum):
    BEFORE_FIRST_SAMPLE = "before_first_sample"
    AFTER_LAST_SAMPLE = "after_last_sample"
    BREAKPOINT = "breakpoint"
    GAP_TOO_LONG = "gap_too_long"
    NO_SAMPLES = "no_samples"


@dataclass(frozen=True)
class SampleRecord:
    sample_id: str
    time: Fraction
    value: Fraction
    seq: int
    retracted: bool = False


@dataclass(frozen=True)
class GridPoint:
    index: int
    time: Fraction
    mode: Mode
    basis: str
    valid: bool
    value: Optional[Decimal] = None
    sample_ids: tuple[str, ...] = ()
    reason: Optional[UnknownReason] = None
    revision: int = 0

    def as_dict(self) -> dict:
        return {
            "index": self.index,
            "time": str(self.time),
            "mode": self.mode.value,
            "basis": self.basis,
            "valid": self.valid,
            "value": None if self.value is None else str(self.value),
            "sample_ids": list(self.sample_ids),
            "reason": None if self.reason is None else self.reason.value,
            "revision": self.revision,
        }


@dataclass(frozen=True)
class Revision:
    index: int
    before: GridPoint
    after: GridPoint


@dataclass(frozen=True)
class ReleaseResult:
    archived_events: int
    sealed_through_index: Optional[int]
    retained_boundary_sample_id: Optional[str]


class RevisionsBlocked(RuntimeError):
    pass


class SealedIntervalError(RuntimeError):
    pass


class CorrectableResampler:
    def __init__(
        self,
        origin: ExactTime,
        period: ExactTime,
        mode: Mode | str,
        max_missing_interval: ExactTime,
    ) -> None:
        self.origin = to_fraction(origin)
        self.period = to_fraction(period)
        if self.period <= 0:
            raise ValueError("period must be positive")
        self.mode = Mode(mode)
        self.max_missing_interval = to_fraction(max_missing_interval)
        if self.max_missing_interval < 0:
            raise ValueError("max_missing_interval must be non-negative")
        self._history: list[SampleRecord] = []
        self._archived: list[SampleRecord] = []
        self._known_ids: set[str] = set()
        self._breakpoints: set[Fraction] = set()
        self._revision = 0
        self._watched: dict[int, GridPoint] = {}
        self._pending: list[Revision] = []
        self._sealed_through_index: Optional[int] = None
        self._sealed_time: Optional[Fraction] = None

    @property
    def revision(self) -> int:
        return self._revision

    @property
    def sealed_through_index(self) -> Optional[int]:
        return self._sealed_through_index

    def time_at(self, index: int) -> Fraction:
        return grid_time(self.origin, self.period, index)

    def index_stats(self) -> dict[str, int]:
        return {
            "history": len(self._history),
            "archived": len(self._archived),
            "breakpoints": len(self._breakpoints),
            "watched": len(self._watched),
            "pending_revisions": len(self._pending),
        }

    def retained_records(self) -> tuple[SampleRecord, ...]:
        return tuple(self._history)

    def history_records(self, include_archived: bool = True) -> tuple[SampleRecord, ...]:
        if include_archived:
            return tuple(self._archived + self._history)
        return tuple(self._history)

    def retained_sample_ids(self) -> tuple[str, ...]:
        return tuple(record.sample_id for record in self._history)

    def history_sample_ids(self, include_archived: bool = True) -> tuple[str, ...]:
        return tuple(
            record.sample_id for record in self.history_records(include_archived)
        )

    def _ensure_open(self, event_time: Fraction) -> None:
        if self._sealed_time is not None and event_time <= self._sealed_time:
            raise SealedIntervalError(
                f"time {event_time} is in a sealed interval ending at {self._sealed_time}"
            )

    @staticmethod
    def _value(value: ExactTime) -> Fraction:
        return to_fraction(value)

    def ingest(
        self,
        sample_id: str,
        time: ExactTime,
        value: ExactTime,
    ) -> list[Revision]:
        if not isinstance(sample_id, str) or not sample_id:
            raise ValueError("sample_id must be a non-empty string")
        if sample_id in self._known_ids:
            raise ValueError(f"sample id {sample_id!r} cannot be reused")
        sample_time = to_fraction(time)
        self._ensure_open(sample_time)
        self._known_ids.add(sample_id)
        self._history.append(
            SampleRecord(
                sample_id=sample_id,
                time=sample_time,
                value=self._value(value),
                seq=len(self._history) + len(self._archived),
            )
        )
        self._history.sort(key=lambda record: (record.time, record.seq))
        return self._mutate()

    def retract(self, sample_id: str) -> list[Revision]:
        for position, record in enumerate(self._history):
            if record.sample_id == sample_id:
                if record.retracted:
                    raise ValueError(f"sample {sample_id!r} is already retracted")
                event_time = record.time
                self._ensure_open(event_time)
                self._history[position] = SampleRecord(
                    sample_id=record.sample_id,
                    time=record.time,
                    value=record.value,
                    seq=record.seq,
                    retracted=True,
                )
                return self._mutate()
        if sample_id in self._known_ids:
            raise SealedIntervalError(
                f"sample {sample_id!r} has already been released and cannot be retracted"
            )
        raise KeyError(sample_id)

    def add_breakpoint(self, time: ExactTime) -> list[Revision]:
        breakpoint_time = to_fraction(time)
        self._ensure_open(breakpoint_time)
        if breakpoint_time in self._breakpoints:
            return []
        self._breakpoints.add(breakpoint_time)
        return self._mutate()

    def _active_winners(self) -> tuple[list[Fraction], dict[Fraction, SampleRecord]]:
        winners: dict[Fraction, SampleRecord] = {}
        for record in self._history:
            if not record.retracted:
                winners[record.time] = record
        times = sorted(winners)
        return times, winners

    def _mutate(self) -> list[Revision]:
        self._revision += 1
        revisions: list[Revision] = []
        for index, before in tuple(self._watched.items()):
            after = self._evaluate(index, self.time_at(index))
            semantic_fields = (
                "time",
                "mode",
                "basis",
                "valid",
                "value",
                "sample_ids",
                "reason",
            )
            changed = any(
                getattr(before, field) != getattr(after, field)
                for field in semantic_fields
            )
            self._watched[index] = after
            if changed:
                revision = Revision(index=index, before=before, after=after)
                self._pending.append(revision)
                revisions.append(revision)
        return revisions

    def drain_revisions(self) -> list[Revision]:
        revisions = self._pending
        self._pending = []
        return revisions

    def watch(self, indices: Iterable[int]) -> list[GridPoint]:
        index_list = list(indices)
        points = [self.point(index) for index in index_list]
        for index, point in zip(index_list, points):
            self._watched[index] = point
        return points

    def unwatch(self, indices: Iterable[int]) -> None:
        for index in indices:
            self._watched.pop(index, None)

    def point(self, index: int) -> GridPoint:
        point = self._evaluate(index, self.time_at(index))
        self._check_sealed_point(index, point)
        return point

    def query_range(self, start_index: int, end_index: int) -> list[GridPoint]:
        if end_index < start_index:
            raise ValueError("end_index must be >= start_index")
        return [self.point(index) for index in range(start_index, end_index + 1)]

    def _check_sealed_point(self, index: int, point: GridPoint) -> None:
        if (
            self._sealed_through_index is not None
            and index <= self._sealed_through_index
        ):
            raise SealedIntervalError(
                f"grid index {index} at {point.time} is sealed and was released"
            )

    def _unknown(
        self,
        index: int,
        point_time: Fraction,
        basis: str,
        reason: UnknownReason,
        sample_ids: Sequence[str] = (),
    ) -> GridPoint:
        return GridPoint(
            index=index,
            time=point_time,
            mode=self.mode,
            basis=basis,
            valid=False,
            value=None,
            sample_ids=tuple(sample_ids),
            reason=reason,
            revision=self._revision,
        )

    def _evaluate(self, index: int, point_time: Fraction) -> GridPoint:
        times, winners = self._active_winners()
        if not times:
            return self._unknown(index, point_time, "none", UnknownReason.NO_SAMPLES)

        direct_position = bisect_left(times, point_time)
        if direct_position < len(times) and times[direct_position] == point_time:
            sample = winners[point_time]
            return GridPoint(
                index=index,
                time=point_time,
                mode=self.mode,
                basis="observed",
                valid=True,
                value=round_output(sample.value),
                sample_ids=(sample.sample_id,),
                revision=self._revision,
            )

        left_position = direct_position - 1
        right_position = direct_position
        left = winners[times[left_position]] if left_position >= 0 else None
        right = winners[times[right_position]] if right_position < len(times) else None

        if self.mode is Mode.LINEAR:
            return self._linear_point(index, point_time, left, right)
        return self._step_point(index, point_time, left, right)

    def _linear_point(
        self,
        index: int,
        point_time: Fraction,
        left: Optional[SampleRecord],
        right: Optional[SampleRecord],
    ) -> GridPoint:
        if left is None:
            return self._unknown(
                index, point_time, "none", UnknownReason.BEFORE_FIRST_SAMPLE
            )
        if right is None:
            return self._unknown(
                index,
                point_time,
                "none",
                UnknownReason.AFTER_LAST_SAMPLE,
                (left.sample_id,),
            )
        sample_ids = (left.sample_id, right.sample_id)
        breakpoint_times = sorted(self._breakpoints)
        first_breakpoint = bisect_right(breakpoint_times, left.time)
        blocked = any(
            left.time < breakpoint < right.time
            for breakpoint in breakpoint_times[first_breakpoint:]
        )
        if blocked:
            return self._unknown(
                index, point_time, "linear", UnknownReason.BREAKPOINT, sample_ids
            )
        if right.time - left.time > self.max_missing_interval:
            return self._unknown(
                index, point_time, "linear", UnknownReason.GAP_TOO_LONG, sample_ids
            )
        value = interpolate_linear(
            point_time, left.time, left.value, right.time, right.value
        )
        return GridPoint(
            index=index,
            time=point_time,
            mode=Mode.LINEAR,
            basis="linear",
            valid=True,
            value=round_output(value),
            sample_ids=sample_ids,
            revision=self._revision,
        )

    def _step_point(
        self,
        index: int,
        point_time: Fraction,
        left: Optional[SampleRecord],
        right: Optional[SampleRecord],
    ) -> GridPoint:
        if left is None:
            return self._unknown(
                index,
                point_time,
                "none",
                UnknownReason.BEFORE_FIRST_SAMPLE,
                (right.sample_id,) if right else (),
            )
        sample_ids = (left.sample_id,)
        if any(
            left.time < breakpoint <= point_time
            for breakpoint in self._breakpoints
        ):
            return self._unknown(
                index, point_time, "step", UnknownReason.BREAKPOINT, sample_ids
            )
        if point_time - left.time > self.max_missing_interval:
            return self._unknown(
                index, point_time, "step", UnknownReason.GAP_TOO_LONG, sample_ids
            )
        return GridPoint(
            index=index,
            time=point_time,
            mode=Mode.STEP,
            basis="step",
            valid=True,
            value=round_output(left.value),
            sample_ids=sample_ids,
            revision=self._revision,
        )

    def release_through(self, index: int) -> ReleaseResult:
        if (
            self._sealed_through_index is not None
            and index <= self._sealed_through_index
        ):
            return ReleaseResult(
                archived_events=0,
                sealed_through_index=self._sealed_through_index,
                retained_boundary_sample_id=self._boundary_sample_id(),
            )

        seal_time = self.time_at(index)
        blocked = [revision for revision in self._pending if revision.index <= index]
        if blocked:
            raise RevisionsBlocked(
                "drain pending revisions for sealed grid points before release"
            )

        times, winners = self._active_winners()
        carry: Optional[SampleRecord] = None
        carry_position = bisect_right(times, seal_time) - 1
        if carry_position >= 0:
            candidate = winners[times[carry_position]]
            closed_breakpoints = [
                breakpoint
                for breakpoint in self._breakpoints
                if breakpoint <= seal_time
            ]
            if not closed_breakpoints or candidate.time >= max(closed_breakpoints):
                carry = candidate

        kept_events: list[SampleRecord] = []
        if carry is not None:
            kept_events.append(
                SampleRecord(
                    sample_id=carry.sample_id,
                    time=carry.time,
                    value=carry.value,
                    seq=carry.seq,
                )
            )
        for record in self._history:
            if record.time > seal_time:
                kept_events.append(record)

        archived = [record for record in self._history if record not in kept_events]
        archived_events = len(archived)
        self._archived.extend(archived)
        self._history = sorted(
            kept_events, key=lambda record: (record.time, record.seq)
        )
        self._breakpoints = {
            breakpoint for breakpoint in self._breakpoints if breakpoint > seal_time
        }
        self._pending = [
            revision for revision in self._pending if revision.index > index
        ]
        for sealed_index in [
            watched for watched in self._watched if watched <= index
        ]:
            del self._watched[sealed_index]
        self._sealed_through_index = index
        self._sealed_time = seal_time
        return ReleaseResult(
            archived_events=archived_events,
            sealed_through_index=index,
            retained_boundary_sample_id=carry.sample_id if carry else None,
        )

    def _boundary_sample_id(self) -> Optional[str]:
        if self._sealed_time is None:
            return None
        times, winners = self._active_winners()
        position = bisect_right(times, self._sealed_time) - 1
        return winners[times[position]].sample_id if position >= 0 else None
