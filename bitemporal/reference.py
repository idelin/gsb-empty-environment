"""Index-free reference implementation.

Answers the same point query by scanning the full record log.  Used as
the ground truth in randomized tests: the index must agree with this on
every query, for every record set and insertion order.
"""

from __future__ import annotations

from typing import Iterable

from .records import VersionRecord, resolution_key


def reference_query(
    records: Iterable[VersionRecord],
    object_id: str,
    valid_time: float,
    as_of: float,
) -> VersionRecord | None:
    best: VersionRecord | None = None
    best_key: tuple | None = None
    for rec in records:
        if rec.object_id != object_id:
            continue
        if not (rec.valid_from <= valid_time < rec.valid_to):
            continue
        if rec.recorded_at > as_of:
            continue
        key = resolution_key(rec)
        if best_key is None or key > best_key:
            best, best_key = rec, key
    return best
