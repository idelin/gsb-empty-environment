"""Missing-input interval bookkeeping shared by engine and reference."""
from __future__ import annotations


def normalize_intervals(intervals) -> list[tuple[int, int]]:
    """Sort and merge half-open [start, stop) intervals."""
    merged: list[tuple[int, int]] = []
    for start, stop in sorted((int(s), int(e)) for s, e in intervals):
        if stop <= start:
            continue
        if merged and start <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], stop))
        else:
            merged.append((start, stop))
    return merged


def intervals_contain(intervals, t: int) -> bool:
    """True if integer index ``t`` falls inside any [start, stop) interval."""
    return any(start <= t < stop for start, stop in intervals)
