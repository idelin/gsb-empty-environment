"""Full-scan verification over the original endpoints.

Every composition, inverse application and merge result is checked by
enumerating the union of the original segment endpoints and comparing
values on each elementary interval -- no sampling, no shortcuts.
"""
from __future__ import annotations

from typing import Iterable, Tuple

from .core import (
    Change,
    Summary,
    Timeline,
    apply,
    invert,
    summarize,
)
from .timeaxis import TimePoint


def endpoints(*timelines: Timeline) -> Tuple[TimePoint, ...]:
    """Sorted union of all original segment endpoints."""
    bounds = set()
    for tl in timelines:
        for seg in tl:
            bounds.add(seg.start)
            bounds.add(seg.end)
    return tuple(sorted(bounds))


def scan_equal(left: Timeline, right: Timeline) -> bool:
    """True iff both timelines agree on every elementary interval."""
    bounds = endpoints(left, right)
    return all(
        left.value_at(lo) == right.value_at(lo)
        for lo, _ in zip(bounds, bounds[1:])
    )


def verify_apply(base: Timeline, changes: Iterable[Change], target: Timeline) -> bool:
    """apply(base, delta) reproduces the target, verified by full scan."""
    return scan_equal(apply(base, changes).timeline, target)


def verify_inverse(base: Timeline, changes: Iterable[Change]) -> bool:
    """Applying the delta and then its inverse restores the base exactly."""
    forward = apply(base, changes)
    restored = apply(forward.timeline, invert(changes))
    return scan_equal(restored.timeline, base)


def verify_summary(timeline: Timeline, summary: Summary) -> bool:
    """The timeline still matches a previously recorded baseline summary."""
    return summarize(timeline) == summary
