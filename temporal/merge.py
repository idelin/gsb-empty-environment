"""Three-way merge and rebase of two independent branch deltas.

``merge3`` combines two deltas that were both produced against the same
base timeline.  Non-overlapping changes and compatible overlapping changes
(both sides agree on the resulting value) merge automatically.  When the
two sides change an overlapping interval to incompatible values, the
minimal conflict segment -- exactly the disagreeing overlap, coalesced
over equal (base, a, b) triples -- is reported, and that region keeps its
base value in the merged timeline.

``rebase`` expresses one delta on top of another: it applies the ``onto``
delta first, merges, and returns the changes still needed to reach the
merged result from the rebased base.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable, Tuple

from .core import (
    MISSING,
    ApplyResult,
    Change,
    ChangeKind,
    Timeline,
    apply,
    check_changes,
    diff,
)
from .timeaxis import TimePoint


@dataclass(frozen=True)
class Conflict:
    """A minimal segment where two branches changed incompatibly."""

    start: TimePoint
    end: TimePoint
    base_value: Any
    a_value: Any
    b_value: Any


@dataclass(frozen=True)
class MergeResult:
    timeline: Timeline
    conflicts: Tuple[Conflict, ...]
    changes: Tuple[Change, ...]  # auto-merged changes that were applied
    copied_nodes: int
    shared_nodes: int

    @property
    def ok(self) -> bool:
        return not self.conflicts


@dataclass(frozen=True)
class RebaseResult:
    onto: Timeline          # base + onto-delta
    merged: Timeline        # base + both deltas (conflict regions keep base)
    changes: Tuple[Change, ...]  # rebased delta: onto -> merged
    conflicts: Tuple[Conflict, ...]

    @property
    def ok(self) -> bool:
        return not self.conflicts


def _covering(changes: Tuple[Change, ...], point: TimePoint):
    for ch in changes:
        if ch.start <= point < ch.end:
            return ch
    return None


def merge3(
    base: Timeline,
    delta_a: Iterable[Change],
    delta_b: Iterable[Change],
) -> MergeResult:
    a_changes = check_changes(base, delta_a)
    b_changes = check_changes(base, delta_b)

    bounds = set()
    for seg in base:
        bounds.add(seg.start)
        bounds.add(seg.end)
    for ch in a_changes + b_changes:
        bounds.add(ch.start)
        bounds.add(ch.end)
    ordered_bounds = sorted(bounds)

    items = []      # (lo, hi, base_v, resolved_v); resolved keeps base on conflict
    conflict_runs = []  # (lo, hi, base_v, a_v, b_v)
    for lo, hi in zip(ordered_bounds, ordered_bounds[1:]):
        base_v = base.value_at(lo)
        ca = _covering(a_changes, lo)
        cb = _covering(b_changes, lo)
        a_v = ca.new if ca is not None else base_v
        b_v = cb.new if cb is not None else base_v
        if ca is not None and cb is not None and a_v != b_v:
            conflict_runs.append((lo, hi, base_v, a_v, b_v))
            items.append((lo, hi, base_v, base_v))
        else:
            items.append((lo, hi, base_v, a_v if ca is not None else b_v))

    conflicts = []
    for lo, hi, base_v, a_v, b_v in conflict_runs:
        if conflicts and conflicts[-1].end == lo and (
            conflicts[-1].base_value,
            conflicts[-1].a_value,
            conflicts[-1].b_value,
        ) == (base_v, a_v, b_v):
            prev = conflicts[-1]
            conflicts[-1] = Conflict(prev.start, hi, base_v, a_v, b_v)
        else:
            conflicts.append(Conflict(lo, hi, base_v, a_v, b_v))

    # Minimal auto-merged changes: group consecutive items by (base, resolved).
    changes = []
    open_key = None
    open_start = None
    prev_hi = None
    for lo, hi, base_v, resolved in items:
        key = (base_v, resolved) if resolved != base_v else None
        if key != open_key:
            if open_key is not None:
                old_v, new_v = open_key
                if old_v is MISSING:
                    kind = ChangeKind.INSERT
                elif new_v is MISSING:
                    kind = ChangeKind.DELETE
                else:
                    kind = ChangeKind.UPDATE
                changes.append(Change(kind, open_start, prev_hi, old_v, new_v))
            open_key = key
            open_start = lo
        prev_hi = hi
    if open_key is not None:
        old_v, new_v = open_key
        if old_v is MISSING:
            kind = ChangeKind.INSERT
        elif new_v is MISSING:
            kind = ChangeKind.DELETE
        else:
            kind = ChangeKind.UPDATE
        changes.append(Change(kind, open_start, prev_hi, old_v, new_v))

    result = apply(base, changes)
    return MergeResult(
        timeline=result.timeline,
        conflicts=tuple(conflicts),
        changes=tuple(changes),
        copied_nodes=result.copied_nodes,
        shared_nodes=result.shared_nodes,
    )


def rebase(
    base: Timeline,
    onto: Iterable[Change],
    changes: Iterable[Change],
) -> RebaseResult:
    """Rebase ``changes`` on top of the ``onto`` delta."""
    onto_changes = check_changes(base, onto)
    onto_result = apply(base, onto_changes)
    merged = merge3(base, onto_changes, changes)
    rebased = diff(onto_result.timeline, merged.timeline)
    return RebaseResult(
        onto=onto_result.timeline,
        merged=merged.timeline,
        changes=rebased,
        conflicts=merged.conflicts,
    )
