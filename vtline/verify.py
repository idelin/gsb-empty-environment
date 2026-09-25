"""Full-scan verification over original endpoints.

Because timelines are piecewise constant with boundaries taken from the
objects involved, sampling one point per atomic interval (plus one point
outside the whole range on each side) is a complete equivalence check.
"""

from __future__ import annotations

from .merge import Conflict
from .model import ChangeSpan, Diff, Segment
from .time_model import Instant
from .timeline import Timeline


def endpoints_of(*objects) -> list:
    """All original endpoints mentioned by the given timelines / diffs /
    spans / conflicts, sorted and deduplicated."""
    pts = set()
    for obj in objects:
        if isinstance(obj, Timeline):
            for s in obj.segments:
                pts.add(s.start.nanos)
                pts.add(s.end.nanos)
        elif isinstance(obj, Diff):
            for c in obj.changes:
                pts.add(c.start.nanos)
                pts.add(c.end.nanos)
        elif isinstance(obj, (Segment, ChangeSpan, Conflict)):
            pts.add(obj.start.nanos)
            pts.add(obj.end.nanos)
        else:
            raise TypeError(f"cannot take endpoints of {obj!r}")
    return [Instant(n) for n in sorted(pts)]


def sample_points(endpoints: list) -> list:
    """One sample per atomic interval plus one sample outside each end."""
    if not endpoints:
        return [Instant(0)]
    pts = [Instant(endpoints[0].nanos - 1)]
    for a, b in zip(endpoints, endpoints[1:]):
        pts.append(a)
        if b.nanos - a.nanos > 1:
            pts.append(Instant((a.nanos + b.nanos) // 2))
    pts.append(endpoints[-1])
    pts.append(Instant(endpoints[-1].nanos + 1))
    return pts


def full_scan_equal(t1: Timeline, t2: Timeline, endpoints: list) -> bool:
    """True iff ``t1`` and ``t2`` agree at every sample point derived from
    the original endpoints."""
    return all(
        t1.entry_at(p) == t2.entry_at(p) for p in sample_points(endpoints)
    )
