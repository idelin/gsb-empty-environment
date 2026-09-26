"""Independent verifier for a BitemporalStore.

The verifier does not trust the index.  It replays the store's operation
log with a naive model (a plain dict of records per version) and, for every
observation version, scans sample points across the whole valid-time line,
comparing the naive expectation against the store's index-backed queries.

It checks, per version:

* **coverage**      -- segments tile the timeline contiguously and version
                       entries tile the version axis from 0 with exactly one
                       open tail (structural check);
* **boundaries**    -- half-open semantics: a point exactly on a boundary
                       belongs to the interval starting there, never to both
                       (checked by sampling exactly on every cut);
* **history invariance** -- the naive model at version ``v`` is built only
                       from operations with version ``<= v``; if the index
                       matches it at every ``v``, later retroactive
                       corrections cannot have altered earlier versions.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .store import BitemporalStore, Upsert, Retract, ChangePriority, _RecordState
from .timekit import Duration, TimePoint


@dataclass
class VerifyReport:
    versions_checked: int = 0
    points_checked: int = 0
    rectangles_checked: int = 0
    errors: list[str] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.errors

    def raise_if_failed(self) -> None:
        if self.errors:
            raise AssertionError(
                f"verification failed with {len(self.errors)} error(s); first: "
                f"{self.errors[0]}"
            )


def _naive_visible(records: dict, point: TimePoint) -> Optional[tuple]:
    covering = [
        (rid, rec)
        for rid, rec in records.items()
        if rec.valid_from <= point
        and (rec.valid_to is None or point < rec.valid_to)
    ]
    if not covering:
        return None
    rid, rec = max(covering, key=lambda item: (item[1].priority, item[0]))
    return (rid, rec.value, rec.priority)


def _sample_points(store: BitemporalStore) -> list[TimePoint]:
    points: set[TimePoint] = set()
    step = Duration.seconds(1)
    for lo, hi, _entries in store._visible.segments():
        if lo is not None:
            points.add(lo)  # exactly on the boundary: must not double-hit
        if lo is not None and hi is not None:
            points.add(lo + (hi - lo) / 2)
        elif lo is not None:
            points.add(lo + step)
        elif hi is not None:
            points.add(hi - step)
        else:
            points.add(TimePoint(0))
    return sorted(points)


def _check_structure(store: BitemporalStore, report: VerifyReport) -> None:
    for name, index in (("visible", store._visible), ("raw", store._raw)):
        segments = list(index.segments())
        for k, (lo, hi, entries) in enumerate(segments):
            if k + 1 < len(segments):
                nlo = segments[k + 1][0]
                if hi != nlo:
                    report.errors.append(
                        f"{name}: segment gap/overlap at boundary {k}: "
                        f"hi={hi!r} next_lo={nlo!r}"
                    )
            if not entries:
                report.errors.append(f"{name}: segment {k} has no entries")
                continue
            if entries[0].version_from != 0:
                report.errors.append(
                    f"{name}: segment {k} entries do not start at version 0"
                )
            for a, b in zip(entries, entries[1:]):
                report.rectangles_checked += 1
                if a.version_to != b.version_from:
                    report.errors.append(
                        f"{name}: segment {k} version gap/overlap "
                        f"{a.version_to} != {b.version_from}"
                    )
            if entries[-1].version_to is not None:
                report.errors.append(f"{name}: segment {k} has no open tail")


def verify_store(store: BitemporalStore) -> VerifyReport:
    report = VerifyReport()
    _check_structure(store, report)

    points = _sample_points(store)
    records: dict[str, _RecordState] = {}

    def check_version(version: int) -> None:
        report.versions_checked += 1
        for point in points:
            report.points_checked += 1
            expected = _naive_visible(records, point)
            got = store.value_at(version, point)
            got_tuple = (
                None if got is None else (got.record_id, got.value, got.priority)
            )
            if got_tuple != expected:
                report.errors.append(
                    f"v{version} @{point.micros}: visible mismatch "
                    f"index={got_tuple!r} expected={expected!r}"
                )
            expected_raw = tuple(sorted(
                rid for rid, rec in records.items()
                if rec.valid_from <= point
                and (rec.valid_to is None or point < rec.valid_to)
            ))
            raw_entry, _, _ = store._raw.point_query(version, point)
            if raw_entry.payload != expected_raw:
                report.errors.append(
                    f"v{version} @{point.micros}: raw mismatch "
                    f"index={raw_entry.payload!r} expected={expected_raw!r}"
                )

    check_version(0)
    for version, ops in store.op_log():
        for op in ops:
            if isinstance(op, Upsert):
                records[op.record_id] = _RecordState(
                    op.valid_from, op.valid_to, op.value, op.priority
                )
            elif isinstance(op, Retract):
                records.pop(op.record_id, None)
            elif isinstance(op, ChangePriority):
                rec = records[op.record_id]
                records[op.record_id] = _RecordState(
                    rec.valid_from, rec.valid_to, rec.value, op.priority
                )
        check_version(version)

    return report
