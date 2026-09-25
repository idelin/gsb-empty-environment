"""Diff, inverse, and composition over timelines.

``diff(old, new)`` emits the minimal set of continuous change spans: atomic
intervals between consecutive original endpoints are classified (add / delete
/ modify) and adjacent spans merge only when kind, before, and after all
match -- so same-value neighbors merge, but different change types never do.
Zero-length changes are never emitted.
"""

from __future__ import annotations

from bisect import bisect_right
from typing import Optional

from .model import ChangeKind, ChangeSpan, Diff, classify_change
from .time_model import Instant
from .timeline import Timeline


def _covering(changes: tuple, instant: Instant) -> Optional[ChangeSpan]:
    starts = [c.start.nanos for c in changes]
    idx = bisect_right(starts, instant.nanos) - 1
    if idx >= 0 and changes[idx].end.nanos > instant.nanos:
        return changes[idx]
    return None


def _append_span(out: list, span: ChangeSpan) -> None:
    """Append, merging with the previous span when kind, before, and after
    all match (same change type and same values only)."""
    if (
        out
        and out[-1].end == span.start
        and out[-1].kind is span.kind
        and out[-1].before == span.before
        and out[-1].after == span.after
    ):
        prev = out[-1]
        out[-1] = ChangeSpan(prev.start, span.end, prev.kind, prev.before, prev.after)
    else:
        out.append(span)


def diff(old: Timeline, new: Timeline) -> Diff:
    """Minimal continuous change spans transforming ``old`` into ``new``."""
    points = sorted(
        {s.start.nanos for s in old.segments}
        | {s.end.nanos for s in old.segments}
        | {s.start.nanos for s in new.segments}
        | {s.end.nanos for s in new.segments}
    )
    changes: list = []
    for a, b in zip(points, points[1:]):
        if a == b:
            continue  # zero-length intervals are never emitted
        ia, ib = Instant(a), Instant(b)
        before = old.entry_at(ia)
        after = new.entry_at(ia)
        kind = classify_change(before, after)
        if kind is None:
            continue
        _append_span(changes, ChangeSpan(ia, ib, kind, before, after))
    return Diff(tuple(changes), base_digest=old.summary().digest)


def inverse(d: Diff, result_digest: Optional[str] = None) -> Diff:
    """The exact reverse transform: applying it to the result of ``d``
    restores the original baseline."""
    swap = {
        ChangeKind.ADD: ChangeKind.DELETE,
        ChangeKind.DELETE: ChangeKind.ADD,
        ChangeKind.MODIFY: ChangeKind.MODIFY,
    }
    changes = tuple(
        ChangeSpan(c.start, c.end, swap[c.kind], c.after, c.before)
        for c in d.changes
    )
    return Diff(changes, base_digest=result_digest)


def compose(first: Diff, second: Diff) -> Diff:
    """Compose two sequential diffs into one equivalent diff.

    Works structurally over the union of both diffs' endpoints; regions
    touched by neither diff produce no change, and regions where the two
    transforms cancel out (before == after) are dropped.
    """
    points = sorted(
        {c.start.nanos for c in first.changes}
        | {c.end.nanos for c in first.changes}
        | {c.start.nanos for c in second.changes}
        | {c.end.nanos for c in second.changes}
    )
    changes: list = []
    for a, b in zip(points, points[1:]):
        if a == b:
            continue
        ia, ib = Instant(a), Instant(b)
        c1 = _covering(first.changes, ia)
        c2 = _covering(second.changes, ia)
        if c1 is None and c2 is None:
            continue
        before = c1.before if c1 is not None else c2.before
        after = c2.after if c2 is not None else c1.after
        kind = classify_change(before, after)
        if kind is None:
            continue
        _append_span(changes, ChangeSpan(ia, ib, kind, before, after))
    return Diff(tuple(changes), base_digest=first.base_digest)
