"""Bitemporal version index: locate versions by object id and both times.

Per object the index keeps the records sorted by ``valid_from`` so a
point query can bisect to the first interval that could cover the valid
time, then scan only the overlapping candidates.  Visibility filtering
(``recorded_at <= as_of``) and the deterministic resolution rule from
:mod:`bitemporal.records` are applied to those candidates.

The index is a pure function of its record *set*: results never depend on
insertion order.  (A production system would layer an interval tree and a
transaction-time column store on top; the semantics here are identical.)
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from typing import Any, Iterable

from .records import Kind, VersionRecord, resolution_key


@dataclass(frozen=True)
class QueryResult:
    """Outcome of a bitemporal point query."""

    object_id: str
    valid_time: float
    as_of: float
    record: VersionRecord | None  # the winning record, None if nothing visible

    @property
    def value(self) -> Any:
        """The value in force, or None if absent/retracted at that point."""
        if self.record is None or self.record.kind is Kind.RETRACT:
            return None
        return self.record.value

    @property
    def retracted(self) -> bool:
        return self.record is not None and self.record.kind is Kind.RETRACT


class BitemporalIndex:
    """Append-only bitemporal index keyed by object id."""

    def __init__(self) -> None:
        # object_id -> records sorted by (valid_from, valid_to, record_id)
        self._by_valid: dict[str, list[VersionRecord]] = {}
        self._next_id = 0

    # ------------------------------------------------------------------ #
    # ingestion
    # ------------------------------------------------------------------ #
    def add(
        self,
        object_id: str,
        valid_from: float,
        valid_to: float,
        recorded_at: float,
        kind: Kind,
        value: Any = None,
        priority: int = 0,
    ) -> VersionRecord:
        """Append one immutable version.  Never mutates existing records."""
        rec = VersionRecord(
            record_id=self._next_id,
            object_id=object_id,
            valid_from=valid_from,
            valid_to=valid_to,
            recorded_at=recorded_at,
            kind=kind,
            value=value,
            priority=priority,
        )
        self._next_id += 1
        self.add_record(rec)
        return rec

    def add_record(self, rec: VersionRecord) -> None:
        lst = self._by_valid.setdefault(rec.object_id, [])
        keys = [(r.valid_from, r.valid_to, r.record_id) for r in lst]
        pos = bisect_right(keys, (rec.valid_from, rec.valid_to, rec.record_id))
        lst.insert(pos, rec)
        self._next_id = max(self._next_id, rec.record_id + 1)

    # ------------------------------------------------------------------ #
    # queries
    # ------------------------------------------------------------------ #
    def query(self, object_id: str, valid_time: float, as_of: float) -> QueryResult:
        """What do we know about ``object_id`` at ``valid_time``,
        given only evidence recorded at or before ``as_of``?"""
        lst = self._by_valid.get(object_id)
        if not lst:
            return QueryResult(object_id, valid_time, as_of, None)
        # bisect: every record with valid_from <= valid_time is a candidate
        starts = [r.valid_from for r in lst]
        hi = bisect_right(starts, valid_time)
        best: VersionRecord | None = None
        best_key: tuple | None = None
        for rec in lst[:hi]:  # valid_from <= valid_time guaranteed
            if rec.valid_to <= valid_time:      # right-open interval
                continue
            if rec.recorded_at > as_of:          # not yet recorded
                continue
            key = resolution_key(rec)
            if best_key is None or key > best_key:
                best, best_key = rec, key
        return QueryResult(object_id, valid_time, as_of, best)

    def history(self, object_id: str, valid_time: float) -> list[VersionRecord]:
        """All versions covering ``valid_time``, oldest knowledge first."""
        lst = self._by_valid.get(object_id, [])
        recs = [r for r in lst if r.covers(valid_time)]
        return sorted(recs, key=resolution_key)

    def records(self, object_id: str | None = None) -> list[VersionRecord]:
        """All stored records (for one object, or the whole store)."""
        if object_id is not None:
            return list(self._by_valid.get(object_id, []))
        return [r for lst in self._by_valid.values() for r in lst]

    def objects(self) -> Iterable[str]:
        return self._by_valid.keys()
