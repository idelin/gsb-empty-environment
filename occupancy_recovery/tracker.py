"""State machine for reconstructing occupancy intervals from start/end records."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, Hashable, Optional, Tuple

from .interval_index import DynamicIntervalIndex, PeakQueryResult


_STATUS_OPEN_START = "open_start"
_STATUS_OPEN_END = "open_end"
_STATUS_ACTIVE = "active"
_STATUS_INVALID = "invalid"
_STATUS_REVOKED = "revoked"


@dataclass(frozen=True)
class OccupancyInterval:
    occupancy_id: Hashable
    start: Any
    end: Any


@dataclass(frozen=True)
class InvalidRecord:
    occupancy_id: Hashable
    start: Any
    end: Any
    reason: str


@dataclass(frozen=True)
class RecordState:
    occupancy_id: Hashable
    status: str
    start: Optional[Any]
    end: Optional[Any]
    interval: Optional[OccupancyInterval]
    invalid_reason: Optional[str] = None


@dataclass(frozen=True)
class OperationResult:
    status: str
    occupancy_id: Hashable
    interval: Optional[OccupancyInterval] = None
    previous_interval: Optional[OccupancyInterval] = None
    rejected_start: Optional[Any] = None
    rejected_end: Optional[Any] = None
    invalid_reason: Optional[str] = None


@dataclass(frozen=True)
class UnmatchedRecord:
    occupancy_id: Hashable
    endpoint: Any


@dataclass(frozen=True)
class OccupancySnapshot:
    intervals: Tuple[OccupancyInterval, ...]
    unmatched_starts: Tuple[UnmatchedRecord, ...]
    unmatched_ends: Tuple[UnmatchedRecord, ...]
    invalid_records: Tuple[InvalidRecord, ...]


class OccupancyTracker:
    """Pairs occupancy records and exposes an incrementally maintained index."""

    RECORDED = "recorded"
    COMPLETED = "completed"
    CORRECTED = "corrected"
    DUPLICATE = "duplicate"
    UNMATCHED_START_UPDATED = "unmatched_start_updated"
    UNMATCHED_END_UPDATED = "unmatched_end_updated"
    INVALID_RECORDED = "invalid_recorded"
    INVALID_UPDATED = "invalid_updated"
    INVALID_REJECTED = "invalid_rejected"
    REVOKED = "revoked"
    RECORD_REVOKED = "record_revoked"
    NOT_FOUND = "not_found"

    STATUS_ACTIVE = "active"
    STATUS_UNMATCHED_START = "unmatched_start"
    STATUS_UNMATCHED_END = "unmatched_end"
    STATUS_INVALID = "invalid"
    STATUS_REVOKED = "revoked"

    def __init__(self, seed: int = 0) -> None:
        self._records: Dict[Hashable, "_Record"] = {}
        self.index = DynamicIntervalIndex(seed=seed)

    def submit_start(self, occupancy_id: Hashable, start: Any) -> OperationResult:
        record = self._records.get(occupancy_id)
        if record is None:
            self._records[occupancy_id] = _Record(
                occupancy_id, start=start, status=_STATUS_OPEN_START
            )
            return OperationResult(self.RECORDED, occupancy_id)
        if record.status == _STATUS_REVOKED:
            return OperationResult(self.RECORD_REVOKED, occupancy_id)
        if record.start == start and record.status != _STATUS_OPEN_END:
            return OperationResult(self.DUPLICATE, occupancy_id, record.interval)

        if record.status == _STATUS_ACTIVE:
            assert record.start is not None and record.end is not None
            if start < record.end:
                previous = record.interval
                self.index.remove_interval(occupancy_id, record.start, record.end)
                record.start = start
                record.status = _STATUS_ACTIVE
                self.index.add_interval(occupancy_id, start, record.end)
                return OperationResult(
                    self.CORRECTED,
                    occupancy_id,
                    record.interval,
                    previous_interval=previous,
                )
            return OperationResult(
                self.INVALID_REJECTED,
                occupancy_id,
                record.interval,
                previous_interval=record.interval,
                rejected_start=start,
                rejected_end=record.end,
                invalid_reason="start_must_be_before_end",
            )

        if record.status == _STATUS_OPEN_END:
            assert record.end is not None
            if start < record.end:
                record.start = start
                record.status = _STATUS_ACTIVE
                self.index.add_interval(occupancy_id, start, record.end)
                return OperationResult(
                    self.COMPLETED, occupancy_id, record.interval
                )
            record.start = start
            record.status = _STATUS_INVALID
            record.invalid_reason = "start_must_be_before_end"
            return OperationResult(
                self.INVALID_RECORDED,
                occupancy_id,
                rejected_start=start,
                rejected_end=record.end,
                invalid_reason=record.invalid_reason,
            )

        if record.status == _STATUS_INVALID:
            assert record.end is not None
            if start < record.end:
                record.start = start
                record.status = _STATUS_ACTIVE
                record.invalid_reason = None
                self.index.add_interval(occupancy_id, start, record.end)
                return OperationResult(
                    self.COMPLETED, occupancy_id, record.interval
                )
            record.start = start
            record.invalid_reason = "start_must_be_before_end"
            return OperationResult(
                self.INVALID_UPDATED,
                occupancy_id,
                rejected_start=start,
                rejected_end=record.end,
                invalid_reason=record.invalid_reason,
            )

        record.start = start
        return OperationResult(self.UNMATCHED_START_UPDATED, occupancy_id)

    def submit_end(self, occupancy_id: Hashable, end: Any) -> OperationResult:
        record = self._records.get(occupancy_id)
        if record is None:
            self._records[occupancy_id] = _Record(
                occupancy_id, end=end, status=_STATUS_OPEN_END
            )
            return OperationResult(self.RECORDED, occupancy_id)
        if record.status == _STATUS_REVOKED:
            return OperationResult(self.RECORD_REVOKED, occupancy_id)
        if record.end == end and record.status != _STATUS_OPEN_START:
            return OperationResult(self.DUPLICATE, occupancy_id, record.interval)

        if record.status == _STATUS_ACTIVE:
            assert record.start is not None and record.end is not None
            if record.start < end:
                previous = record.interval
                self.index.remove_interval(occupancy_id, record.start, record.end)
                record.end = end
                record.status = _STATUS_ACTIVE
                self.index.add_interval(occupancy_id, record.start, end)
                return OperationResult(
                    self.CORRECTED,
                    occupancy_id,
                    record.interval,
                    previous_interval=previous,
                )
            return OperationResult(
                self.INVALID_REJECTED,
                occupancy_id,
                record.interval,
                previous_interval=record.interval,
                rejected_start=record.start,
                rejected_end=end,
                invalid_reason="end_must_be_after_start",
            )

        if record.status == _STATUS_OPEN_START:
            assert record.start is not None
            if record.start < end:
                record.end = end
                record.status = _STATUS_ACTIVE
                self.index.add_interval(occupancy_id, record.start, end)
                return OperationResult(
                    self.COMPLETED, occupancy_id, record.interval
                )
            record.end = end
            record.status = _STATUS_INVALID
            record.invalid_reason = "end_must_be_after_start"
            return OperationResult(
                self.INVALID_RECORDED,
                occupancy_id,
                rejected_start=record.start,
                rejected_end=end,
                invalid_reason=record.invalid_reason,
            )

        if record.status == _STATUS_INVALID:
            assert record.start is not None
            if record.start < end:
                record.end = end
                record.status = _STATUS_ACTIVE
                record.invalid_reason = None
                self.index.add_interval(occupancy_id, record.start, end)
                return OperationResult(
                    self.COMPLETED, occupancy_id, record.interval
                )
            record.end = end
            record.invalid_reason = "end_must_be_after_start"
            return OperationResult(
                self.INVALID_UPDATED,
                occupancy_id,
                rejected_start=record.start,
                rejected_end=end,
                invalid_reason=record.invalid_reason,
            )

        record.end = end
        return OperationResult(self.UNMATCHED_END_UPDATED, occupancy_id)

    def replace_interval(
        self,
        occupancy_id: Hashable,
        start: Any,
        end: Any,
    ) -> OperationResult:
        """Atomically correct both endpoints of one occupancy record."""

        record = self._records.get(occupancy_id)
        if record is None:
            return OperationResult(self.NOT_FOUND, occupancy_id)
        if record.status == _STATUS_REVOKED:
            return OperationResult(self.RECORD_REVOKED, occupancy_id)
        if (
            record.status == _STATUS_ACTIVE
            and record.start == start
            and record.end == end
        ):
            return OperationResult(self.DUPLICATE, occupancy_id, record.interval)
        if (
            record.status == _STATUS_INVALID
            and record.start == start
            and record.end == end
        ):
            return OperationResult(self.DUPLICATE, occupancy_id)

        if not start < end:
            if record.status == _STATUS_ACTIVE:
                return OperationResult(
                    self.INVALID_REJECTED,
                    occupancy_id,
                    record.interval,
                    previous_interval=record.interval,
                    rejected_start=start,
                    rejected_end=end,
                    invalid_reason="start_must_be_before_end",
                )
            record.start = start
            record.end = end
            record.status = _STATUS_INVALID
            record.invalid_reason = "start_must_be_before_end"
            return OperationResult(
                self.INVALID_UPDATED,
                occupancy_id,
                rejected_start=start,
                rejected_end=end,
                invalid_reason=record.invalid_reason,
            )

        previous = record.interval if record.status == _STATUS_ACTIVE else None
        if record.status == _STATUS_ACTIVE:
            assert record.start is not None and record.end is not None
            self.index.remove_interval(occupancy_id, record.start, record.end)

        record.start = start
        record.end = end
        record.status = _STATUS_ACTIVE
        record.invalid_reason = None
        self.index.add_interval(occupancy_id, start, end)
        status = self.CORRECTED if previous is not None else self.COMPLETED
        return OperationResult(
            status,
            occupancy_id,
            record.interval,
            previous_interval=previous,
        )

    def withdraw(self, occupancy_id: Hashable) -> OperationResult:
        record = self._records.get(occupancy_id)
        if record is None:
            return OperationResult(self.NOT_FOUND, occupancy_id)
        if record.status == _STATUS_REVOKED:
            return OperationResult(self.DUPLICATE, occupancy_id)

        previous = record.interval
        if record.status == _STATUS_ACTIVE:
            assert record.start is not None and record.end is not None
            self.index.remove_interval(occupancy_id, record.start, record.end)
        record.status = _STATUS_REVOKED
        record.invalid_reason = None
        return OperationResult(
            self.REVOKED, occupancy_id, previous_interval=previous
        )

    def get_interval(self, occupancy_id: Hashable) -> Optional[OccupancyInterval]:
        record = self._records.get(occupancy_id)
        return None if record is None else record.interval

    def get_state(self, occupancy_id: Hashable) -> Optional[RecordState]:
        record = self._records.get(occupancy_id)
        if record is None:
            return None
        return RecordState(
            record.occupancy_id,
            self._public_status(record.status),
            record.start,
            record.end,
            record.interval,
            record.invalid_reason,
        )

    def snapshot(self) -> OccupancySnapshot:
        intervals = []
        unmatched_starts = []
        unmatched_ends = []
        invalid_records = []

        for record in self._records.values():
            if record.status == _STATUS_ACTIVE and record.interval is not None:
                intervals.append(record.interval)
            elif record.status == _STATUS_OPEN_START:
                unmatched_starts.append(
                    UnmatchedRecord(record.occupancy_id, record.start)
                )
            elif record.status == _STATUS_OPEN_END:
                unmatched_ends.append(
                    UnmatchedRecord(record.occupancy_id, record.end)
                )
            elif record.status == _STATUS_INVALID:
                invalid_records.append(
                    InvalidRecord(
                        record.occupancy_id,
                        record.start,
                        record.end,
                        record.invalid_reason or "invalid_interval",
                    )
                )

        return OccupancySnapshot(
            tuple(sorted(intervals, key=lambda item: (item.start, repr(item.occupancy_id)))),
            tuple(sorted(unmatched_starts, key=lambda item: repr(item.occupancy_id))),
            tuple(sorted(unmatched_ends, key=lambda item: repr(item.occupancy_id))),
            tuple(sorted(invalid_records, key=lambda item: repr(item.occupancy_id))),
        )

    def peak(self, start: Any, end: Any) -> PeakQueryResult:
        return self.index.query(start, end)

    @staticmethod
    def _public_status(status: str) -> str:
        return {
            _STATUS_ACTIVE: OccupancyTracker.STATUS_ACTIVE,
            _STATUS_OPEN_START: OccupancyTracker.STATUS_UNMATCHED_START,
            _STATUS_OPEN_END: OccupancyTracker.STATUS_UNMATCHED_END,
            _STATUS_INVALID: OccupancyTracker.STATUS_INVALID,
            _STATUS_REVOKED: OccupancyTracker.STATUS_REVOKED,
        }[status]


class _Record:
    __slots__ = ("occupancy_id", "start", "end", "status", "invalid_reason")

    def __init__(
        self,
        occupancy_id: Hashable,
        start: Optional[Any] = None,
        end: Optional[Any] = None,
        status: str = _STATUS_OPEN_START,
    ) -> None:
        self.occupancy_id = occupancy_id
        self.start = start
        self.end = end
        self.status = status
        self.invalid_reason: Optional[str] = None

    @property
    def interval(self) -> Optional[OccupancyInterval]:
        if self.status != _STATUS_ACTIVE or self.start is None or self.end is None:
            return None
        return OccupancyInterval(self.occupancy_id, self.start, self.end)
