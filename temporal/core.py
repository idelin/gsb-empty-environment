"""Versioned valid-time timelines: segments, diffs, reversible application.

A ``Timeline`` is an immutable, coalesced sequence of half-open
``[start, end)`` segments.  Snapshots share unchanged segment nodes by
object identity; ``apply`` reports exactly how many nodes were copied and
how many were shared.

``diff`` produces the minimal continuous change segments between two
timelines, distinguishing INSERT / DELETE / UPDATE.  Adjacent elementary
intervals merge only when their (kind, old, new) triple is identical, so
same-value neighbours coalesce but different change kinds never do.
Zero-length changes are never emitted (and cannot even be constructed).
"""
from __future__ import annotations

import hashlib
from bisect import bisect_right
from dataclasses import dataclass
from enum import Enum
from typing import Any, Iterable, Iterator, Optional, Tuple

from .timeaxis import TimePoint


class _Missing:
    _instance = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "MISSING"


MISSING = _Missing()


@dataclass(frozen=True)
class Segment:
    """A half-open [start, end) interval carrying a value."""

    start: TimePoint
    end: TimePoint
    value: Any

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError("zero-length or inverted segment is not allowed")


class Timeline:
    """Immutable coalesced sequence of non-overlapping segments."""

    __slots__ = ("_segments", "_starts")

    def __init__(self, segments: Iterable[Segment] = ()):
        raw = tuple(segments)
        for prev, nxt in zip(raw, raw[1:]):
            if nxt.start < prev.end:
                raise ValueError("segments overlap or are out of order")
        coalesced = []
        for seg in raw:
            if coalesced and coalesced[-1].end == seg.start and coalesced[-1].value == seg.value:
                # Adjacent equal-value segments merge into one node.
                coalesced[-1] = Segment(coalesced[-1].start, seg.end, seg.value)
            else:
                coalesced.append(seg)
        self._segments = tuple(coalesced)
        self._starts = tuple(s.start for s in self._segments)

    @property
    def segments(self) -> Tuple[Segment, ...]:
        return self._segments

    def __iter__(self) -> Iterator[Segment]:
        return iter(self._segments)

    def __len__(self) -> int:
        return len(self._segments)

    def __eq__(self, other) -> bool:
        return isinstance(other, Timeline) and self._segments == other._segments

    def __hash__(self) -> int:
        return hash(self._segments)

    def __repr__(self) -> str:
        body = ", ".join(
            f"[{s.start.ticks}, {s.end.ticks})->{s.value!r}" for s in self._segments
        )
        return f"Timeline({body})"

    def value_at(self, point: TimePoint) -> Any:
        """Value at ``point``, or MISSING if no segment covers it."""
        idx = bisect_right(self._starts, point) - 1
        if idx < 0:
            return MISSING
        seg = self._segments[idx]
        return seg.value if seg.start <= point < seg.end else MISSING

    def value_over(self, start: TimePoint, end: TimePoint) -> Any:
        """The constant value over [start, end); raises if it varies."""
        probes = [start]
        probes.extend(s.start for s in self._segments if start < s.start < end)
        values = {self.value_at(p) for p in probes}
        if len(values) != 1:
            raise ValueError(f"value is not constant over [{start}, {end})")
        return values.pop()


class ChangeKind(Enum):
    INSERT = "insert"
    DELETE = "delete"
    UPDATE = "update"


@dataclass(frozen=True)
class Change:
    """One minimal continuous change segment.

    ``old``/``new`` are MISSING for the absent side of INSERT/DELETE.
    ``identity`` ties corrections together so they can be superseded or
    retracted by identity rather than by comparing current values.
    """

    kind: ChangeKind
    start: TimePoint
    end: TimePoint
    old: Any
    new: Any
    identity: Optional[str] = None

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError("zero-length change is not allowed")
        if self.kind is ChangeKind.INSERT:
            if self.old is not MISSING or self.new is MISSING:
                raise ValueError("INSERT requires old=MISSING and a new value")
        elif self.kind is ChangeKind.DELETE:
            if self.new is not MISSING or self.old is MISSING:
                raise ValueError("DELETE requires new=MISSING and an old value")
        else:
            if self.old is MISSING or self.new is MISSING or self.old == self.new:
                raise ValueError("UPDATE requires two distinct present values")

    def with_identity(self, identity: str) -> "Change":
        return Change(self.kind, self.start, self.end, self.old, self.new, identity)


@dataclass(frozen=True)
class Summary:
    """Baseline summary of a timeline: digest, node count and span."""

    digest: str
    nodes: int
    start: Optional[TimePoint]
    end: Optional[TimePoint]


def summarize(timeline: Timeline) -> Summary:
    hasher = hashlib.sha256()
    for seg in timeline:
        hasher.update(str(seg.start.ticks).encode())
        hasher.update(b"\x00")
        hasher.update(str(seg.end.ticks).encode())
        hasher.update(b"\x00")
        hasher.update(repr(seg.value).encode())
        hasher.update(b"\x01")
    start = timeline.segments[0].start if len(timeline) else None
    end = timeline.segments[-1].end if len(timeline) else None
    return Summary(hasher.hexdigest(), len(timeline), start, end)


def _elementary_bounds(*timelines: Timeline) -> Tuple[TimePoint, ...]:
    bounds = set()
    for tl in timelines:
        for seg in tl:
            bounds.add(seg.start)
            bounds.add(seg.end)
    return tuple(sorted(bounds))


def diff(old: Timeline, new: Timeline) -> Tuple[Change, ...]:
    """Minimal continuous change segments transforming ``old`` into ``new``.

    Consecutive elementary intervals coalesce only when kind, old value and
    new value all match; different change kinds are never merged.
    """
    bounds = _elementary_bounds(old, new)
    changes = []
    open_key = None
    open_start = None
    for lo, hi in zip(bounds, bounds[1:]):
        old_v = old.value_at(lo)
        new_v = new.value_at(lo)
        if old_v is MISSING and new_v is MISSING:
            key = None
        elif old_v is MISSING:
            key = (ChangeKind.INSERT, None, new_v)
        elif new_v is MISSING:
            key = (ChangeKind.DELETE, old_v, None)
        elif old_v != new_v:
            key = (ChangeKind.UPDATE, old_v, new_v)
        else:
            key = None
        if key != open_key:
            if open_key is not None:
                kind, ov, nv = open_key
                changes.append(
                    Change(kind, open_start, lo, MISSING if ov is None else ov,
                           MISSING if nv is None else nv)
                )
            open_key = key
            open_start = lo
    if open_key is not None:
        kind, ov, nv = open_key
        changes.append(
            Change(kind, open_start, bounds[-1], MISSING if ov is None else ov,
                   MISSING if nv is None else nv)
        )
    return tuple(changes)


@dataclass(frozen=True)
class ApplyResult:
    timeline: Timeline
    copied_nodes: int  # nodes newly allocated for this snapshot
    shared_nodes: int  # nodes reused unchanged from the base snapshot


def check_changes(base: Timeline, changes: Iterable[Change]) -> Tuple[Change, ...]:
    """Sort, validate non-overlap, and verify ``old`` against the base."""
    ordered = tuple(sorted(changes, key=lambda c: (c.start, c.end)))
    for prev, nxt in zip(ordered, ordered[1:]):
        if nxt.start < prev.end:
            raise ValueError("changes overlap")
    for ch in ordered:
        actual = base.value_over(ch.start, ch.end)
        if actual != ch.old:
            raise ValueError(
                f"change {ch!r} does not match base value {actual!r}"
            )
    return ordered


def apply(base: Timeline, changes: Iterable[Change], *, check: bool = True) -> ApplyResult:
    """Apply a delta, reusing unchanged base nodes (structural sharing)."""
    ordered = check_changes(base, changes) if check else tuple(
        sorted(changes, key=lambda c: (c.start, c.end))
    )
    bounds = set()
    for seg in base:
        bounds.add(seg.start)
        bounds.add(seg.end)
    for ch in ordered:
        bounds.add(ch.start)
        bounds.add(ch.end)
    ordered_bounds = sorted(bounds)

    runs = []  # (start, end, value) with value never MISSING
    for lo, hi in zip(ordered_bounds, ordered_bounds[1:]):
        covering = None
        for ch in ordered:
            if ch.start <= lo < ch.end:
                covering = ch
                break
        value = covering.new if covering is not None else base.value_at(lo)
        if value is MISSING:
            continue
        if runs and runs[-1][1] == lo and runs[-1][2] == value:
            runs[-1] = (runs[-1][0], hi, value)
        else:
            runs.append((lo, hi, value))

    base_index = {(s.start, s.end): s for s in base}
    segments = []
    shared = 0
    for lo, hi, value in runs:
        existing = base_index.get((lo, hi))
        if existing is not None and existing.value == value:
            segments.append(existing)
            shared += 1
        else:
            segments.append(Segment(lo, hi, value))
    return ApplyResult(Timeline(segments), len(segments) - shared, shared)


def invert(changes: Iterable[Change]) -> Tuple[Change, ...]:
    """Inverse delta: applying it to the result restores the base."""
    inverted = []
    for ch in changes:
        if ch.kind is ChangeKind.INSERT:
            inverted.append(
                Change(ChangeKind.DELETE, ch.start, ch.end, ch.new, MISSING, ch.identity)
            )
        elif ch.kind is ChangeKind.DELETE:
            inverted.append(
                Change(ChangeKind.INSERT, ch.start, ch.end, MISSING, ch.old, ch.identity)
            )
        else:
            inverted.append(
                Change(ChangeKind.UPDATE, ch.start, ch.end, ch.new, ch.old, ch.identity)
            )
    return tuple(inverted)


class Delta:
    """An identified set of changes with correction and retraction.

    A correction is a change carrying the identity of an earlier change; it
    supersedes it.  ``retract`` removes the identified change entirely, so
    the base state is restored from history -- the retraction is by
    identity, never by matching whatever value happens to be current.
    """

    def __init__(self, changes: Iterable[Change] = ()):
        self._by_id = {}
        self._counter = 0
        for ch in changes:
            self.add(ch)

    def add(self, change: Change) -> Change:
        """Add a change; an existing identity supersedes (a correction)."""
        identity = change.identity
        if identity is None:
            identity = f"ch-{self._counter}"
            self._counter += 1
            change = change.with_identity(identity)
        self._by_id[identity] = change
        return change

    def correct(self, change: Change) -> Change:
        """Supersede the change that shares this change's identity."""
        if change.identity is None or change.identity not in self._by_id:
            raise KeyError("a correction must name an existing identity")
        self._by_id[change.identity] = change
        return change

    def retract(self, identity: str) -> Change:
        """Withdraw the identified change, restoring the base state."""
        return self._by_id.pop(identity)

    def changes(self) -> Tuple[Change, ...]:
        return tuple(sorted(self._by_id.values(), key=lambda c: (c.start, c.end)))

    def __len__(self) -> int:
        return len(self._by_id)
