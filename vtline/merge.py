"""Three-way merge of two independent branch diffs, and rebase."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .diff import _covering, diff
from .model import Diff, Entry, Segment
from .time_model import Instant
from .timeline import BaselineMismatchError, Timeline


@dataclass(frozen=True)
class Conflict:
    """A minimal interval where two branches changed the same content in
    incompatible ways."""

    start: Instant
    end: Instant
    base: Optional[Entry]
    left: Optional[Entry]
    right: Optional[Entry]


@dataclass(frozen=True)
class MergeResult:
    timeline: Timeline
    diff: Diff
    conflicts: tuple


@dataclass(frozen=True)
class RebaseResult:
    diff: Diff
    conflicts: tuple
    timeline: Timeline


def _append_conflict(out: list, start, end, base, left, right) -> None:
    """Append, merging with the previous conflict when adjacent and carrying
    the same (base, left, right) triple -- yields minimal conflict segments."""
    if (
        out
        and out[-1].end == start
        and out[-1].base == base
        and out[-1].left == left
        and out[-1].right == right
    ):
        prev = out[-1]
        out[-1] = Conflict(prev.start, end, base, left, right)
    else:
        out.append(Conflict(start, end, base, left, right))


def merge_three_way(base: Timeline, left: Diff, right: Diff) -> MergeResult:
    """Merge two independent diffs that were both computed against ``base``.

    Regions changed by only one side take that side; regions changed
    identically by both sides take the shared value; overlapping regions with
    incompatible value changes are reported as minimal conflict segments and
    keep the base content in the merged timeline.
    """
    digest = base.summary().digest
    for name, d in (("left", left), ("right", right)):
        if d.base_digest is not None and d.base_digest != digest:
            raise BaselineMismatchError(f"{name} diff was not computed against base")

    points = sorted(
        {s.start.nanos for s in base.segments}
        | {s.end.nanos for s in base.segments}
        | {c.start.nanos for c in left.changes}
        | {c.end.nanos for c in left.changes}
        | {c.start.nanos for c in right.changes}
        | {c.end.nanos for c in right.changes}
    )
    raw: list = []
    conflicts: list = []
    for a, b in zip(points, points[1:]):
        if a == b:
            continue
        ia, ib = Instant(a), Instant(b)
        bval = base.entry_at(ia)
        lc = _covering(left.changes, ia)
        rc = _covering(right.changes, ia)
        lval = lc.after if lc is not None else bval
        rval = rc.after if rc is not None else bval
        if lval == rval:
            chosen = lval
        elif lval == bval:
            chosen = rval
        elif rval == bval:
            chosen = lval
        else:
            chosen = bval
            _append_conflict(conflicts, ia, ib, bval, lval, rval)
        if chosen is not None:
            raw.append(Segment(ia, ib, chosen))
    merged = Timeline(raw)
    return MergeResult(merged, diff(base, merged), tuple(conflicts))


def rebase(moving: Diff, old_base: Timeline, new_base: Timeline) -> RebaseResult:
    """Relocate ``moving`` (computed against ``old_base``) onto ``new_base``.

    Implemented as a three-way merge of ``moving`` with the diff between the
    two bases, then re-expressed as a diff applicable to ``new_base``.
    """
    other = diff(old_base, new_base)
    merged = merge_three_way(old_base, moving, other)
    return RebaseResult(
        diff=diff(new_base, merged.timeline),
        conflicts=merged.conflicts,
        timeline=merged.timeline,
    )
