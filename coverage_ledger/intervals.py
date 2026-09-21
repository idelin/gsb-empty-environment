"""Half-open interval arithmetic used by the coverage ledger.

Intervals are ``[start, end)`` tuples.  A membership rule may stay in force
forever, in which case ``end`` is ``None`` meaning positive infinity.

Only ordering, addition and subtraction are required of the endpoint values,
so ``int``, ``float`` and ``fractions.Fraction`` all work.
"""

from __future__ import annotations

from typing import List, Optional, Sequence, Tuple

Interval = Tuple[object, Optional[object]]


def check_interval(interval: Interval, *, allow_open_end: bool = False) -> Interval:
    """Validate and normalise a ``[start, end)`` interval."""
    if not isinstance(interval, (tuple, list)) or len(interval) != 2:
        raise ValueError(f"interval must be a (start, end) pair, got {interval!r}")
    start, end = tuple(interval)
    if start is None:
        raise ValueError("interval start must be finite")
    if end is None:
        if not allow_open_end:
            raise ValueError("interval end must be finite")
    elif end < start:
        raise ValueError(f"interval end {end!r} is before start {start!r}")
    return (start, end)


def is_empty(interval: Interval) -> bool:
    """A half-open interval [a, a) has zero length and is empty."""
    start, end = interval
    return end is not None and end == start


def merge(intervals: Sequence[Interval]) -> List[Interval]:
    """Union half-open intervals; touching ends are joined.

    ``[0, 4)`` and ``[4, 8)`` merge into ``[0, 8)``: the boundary point 4 is
    not double counted and there is no gap between the two pieces.
    """
    valid = sorted(
        (check_interval(item, allow_open_end=True) for item in intervals if item is not None),
        key=lambda item: (item[0], item[1] is not None, item[1]),
    )
    merged: List[List[object]] = []
    for start, end in valid:
        if not merged:
            merged.append([start, end])
            continue
        last_start, last_end = merged[-1]
        if last_end is not None and start > last_end:
            merged.append([start, end])
        elif end is None:
            last_end_cursor = merged[-1]
            last_end_cursor[1] = None
        elif last_end is not None and end > last_end:
            merged[-1][1] = end
    return [(start, end) for start, end in merged]


def clip(intervals: Sequence[Interval], window: Interval) -> List[Interval]:
    """Restrict intervals to the finite window ``[lo, hi)``."""
    lo, hi = check_interval(window)
    out: List[Interval] = []
    for start, end in merge(intervals):
        if end is not None and end <= lo:
            continue
        if start >= hi:
            continue
        clipped_start = max(start, lo)
        clipped_end = hi if end is None else min(end, hi)
        if clipped_end > clipped_start:
            out.append((clipped_start, clipped_end))
    return out


def intersect_lists(left: Sequence[Interval], right: Sequence[Interval]) -> List[Interval]:
    """Intersection of two merged, finite interval lists."""
    out: List[Interval] = []
    i = j = 0
    while i < len(left) and j < len(right):
        ls, le = left[i]
        rs, re = right[j]
        start = max(ls, rs)
        end = min(le, re)
        if end > start:
            out.append((start, end))
        if le <= re:
            i += 1
        if re <= le:
            j += 1
    return out


def intersect_interval(interval: Interval, other: Interval) -> Optional[Interval]:
    """Intersect two intervals, either of which may have an open right end."""
    start = max(interval[0], other[0])
    finite_ends = [end for end in (interval[1], other[1]) if end is not None]
    end = min(finite_ends) if finite_ends else None
    if end is None or end > start:
        return (start, end)
    return None


def subtract(intervals: Sequence[Interval], removed: Sequence[Interval]) -> List[Interval]:
    """Return ``intervals`` minus the (merged) ``removed`` intervals."""
    holes = merge(removed)
    result: List[Interval] = []
    for start, end in merge(intervals):
        cursor = start
        for hole_start, hole_end in holes:
            if hole_end is not None and hole_end <= cursor:
                continue
            if end is not None and hole_start >= end:
                break
            cut_start = max(cursor, hole_start)
            if cut_start > cursor:
                result.append((cursor, cut_start))
            cursor = end if hole_end is None else hole_end
            if end is not None and cursor >= end:
                break
        if end is None:
            result.append((cursor, None))
        elif cursor < end:
            result.append((cursor, end))
    return merge(result)


def symmetric_difference(left: Sequence[Interval], right: Sequence[Interval]) -> List[Interval]:
    """Regions covered by exactly one of the two interval sets."""
    return subtract(merge(left), right) + subtract(merge(right), left)


def length(intervals: Sequence[Interval]) -> object:
    """Total length of merged intervals (an open-ended interval raises)."""
    total = 0
    for start, end in merge(intervals):
        if end is None:
            raise ValueError("cannot measure an interval with no end")
        total += end - start
    return total
