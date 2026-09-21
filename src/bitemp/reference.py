"""Reference queries: deliberately index-free brute force.

These functions are the oracle for the randomized tests.  They scan the
whole version log and apply the semantic rules directly, so a bug in the
index cannot hide behind shared code.
"""

from __future__ import annotations

from typing import Any, Iterable

from .model import Version


def reference_query(
    versions: Iterable[Version], object_id: str, t_valid: float, t_tx: float
) -> Version | None:
    """The visible evidence for ``object_id`` at business time ``t_valid``
    as known at observation time ``t_tx`` (or None if nothing is recorded).

    Visible = recorded not later than the observation moment.  Among visible
    versions covering ``t_valid`` the one with the highest explicit priority
    (tx_from, seq) wins; a winning retraction is returned as the tombstone
    version itself.
    """
    best: Version | None = None
    for v in versions:
        if v.object_id != object_id:
            continue
        if v.tx_from > t_tx:            # not yet recorded at observation time
            continue
        if not v.covers(t_valid):       # business time outside the interval
            continue
        if best is None or v.priority > best.priority:
            best = v
    return best


def reference_value(
    versions: Iterable[Version], object_id: str, t_valid: float, t_tx: float
) -> Any:
    v = reference_query(versions, object_id, t_valid, t_tx)
    return None if v is None or v.is_tombstone else v.value


def reference_timeline(
    versions: Iterable[Version], object_id: str, t_tx: float
) -> list[tuple[float, float, Version | None]]:
    """Resolved business-time axis as known at ``t_tx``.

    Returns merged, non-overlapping (valid_from, valid_to, winner) triples
    covering the union of all visible intervals; winner None marks a gap
    between recorded intervals.
    """
    visible = [
        v for v in versions if v.object_id == object_id and v.tx_from <= t_tx
    ]
    points = sorted({p for v in visible for p in (v.valid_from, v.valid_to)})
    out: list[tuple[float, float, Version | None]] = []
    for lo, hi in zip(points, points[1:]):
        best: Version | None = None
        for v in visible:
            if v.valid_from <= lo and v.valid_to >= hi:
                if best is None or v.priority > best.priority:
                    best = v
        if out and out[-1][2] is best and out[-1][1] == lo:
            out[-1] = (out[-1][0], hi, best)
        else:
            out.append((lo, hi, best))
    return out
