"""Persistent versioned valid-time timeline.

A :class:`Timeline` is an immutable, piecewise-constant mapping from
``Instant`` to ``Entry`` (gaps mean "no fact").  Applying a diff produces a
new timeline that structurally shares every untouched segment node with the
old one; the number of actually copied (newly allocated) nodes is reported in
:class:`ApplyStats`.
"""

from __future__ import annotations

import hashlib
from bisect import bisect_right
from dataclasses import dataclass
from typing import Optional

from .model import Diff, Entry, Segment
from .time_model import Instant


class BaselineMismatchError(ValueError):
    """Raised when a diff is applied to a timeline whose summary digest does
    not match the digest recorded in the diff."""


@dataclass(frozen=True)
class Summary:
    """Baseline summary of a timeline: content digest plus shape facts."""

    digest: str
    segment_count: int
    first_start: Optional[Instant]
    last_end: Optional[Instant]


@dataclass(frozen=True)
class ApplyStats:
    """Node-level accounting for one apply: how many segment nodes were
    actually copied (newly allocated) versus shared with the old snapshot."""

    copied_nodes: int
    shared_nodes: int


def _normalize(segments: list) -> list:
    """Sort, drop zero-length pieces, reject overlaps, and merge adjacent
    pieces that carry an equal entry (same identity and value)."""
    ordered = sorted(segments, key=lambda s: (s.start.nanos, s.end.nanos))
    out: list = []
    for seg in ordered:
        if seg.end.nanos < seg.start.nanos:
            raise ValueError(f"negative-length segment: {seg!r}")
        if seg.end.nanos == seg.start.nanos:
            continue  # zero-length pieces carry no information
        if out and seg.start.nanos < out[-1].end.nanos:
            raise ValueError(f"overlapping segments: {out[-1]!r} vs {seg!r}")
        if out and out[-1].end == seg.start and out[-1].entry == seg.entry:
            out[-1] = Segment(out[-1].start, seg.end, seg.entry)
        else:
            out.append(seg)
    return out


def _merge_adjacent(sorted_segments: list) -> list:
    """Merge adjacent pieces with equal entries; input must already be sorted
    and non-overlapping (used on the apply() fast path)."""
    out: list = []
    for seg in sorted_segments:
        if out and out[-1].end == seg.start and out[-1].entry == seg.entry:
            out[-1] = Segment(out[-1].start, seg.end, seg.entry)
        else:
            out.append(seg)
    return out


class Timeline:
    """Immutable piecewise-constant map from Instant to Entry."""

    __slots__ = ("_segments", "_starts")

    def __init__(self, segments=()):
        normalized = _normalize(list(segments))
        self._segments = tuple(normalized)
        self._starts = [s.start.nanos for s in normalized]

    @classmethod
    def _from_normalized(cls, segments):
        tl = cls.__new__(cls)
        tl._segments = segments
        tl._starts = [s.start.nanos for s in segments]
        return tl

    @property
    def segments(self):
        return self._segments

    def entry_at(self, instant: Instant) -> Optional[Entry]:
        idx = bisect_right(self._starts, instant.nanos) - 1
        if idx >= 0 and self._segments[idx].end.nanos > instant.nanos:
            return self._segments[idx].entry
        return None

    def summary(self) -> Summary:
        """Baseline summary: a stable digest over every segment's endpoints,
        identity, and value."""
        h = hashlib.sha256()
        for seg in self._segments:
            h.update(
                f"{seg.start.nanos}|{seg.end.nanos}|"
                f"{seg.entry.identity}|{seg.entry.value!r};".encode("utf-8")
            )
        return Summary(
            digest=h.hexdigest(),
            segment_count=len(self._segments),
            first_start=self._segments[0].start if self._segments else None,
            last_end=self._segments[-1].end if self._segments else None,
        )

    def apply(self, diff: Diff):
        """Apply a diff, returning ``(new_timeline, ApplyStats)``.

        The baseline summary is verified against ``diff.base_digest`` first.
        Untouched segment nodes are reused as-is (structural sharing); only
        clipped, inserted, or merged nodes are newly allocated.
        """
        if diff.base_digest is not None and diff.base_digest != self.summary().digest:
            raise BaselineMismatchError(
                "diff baseline digest does not match this timeline's summary"
            )
        changes = sorted(diff.changes, key=lambda c: (c.start.nanos, c.end.nanos))
        for c in changes:
            if not c.end.nanos > c.start.nanos:
                raise ValueError(f"zero-length change span is not applicable: {c!r}")
        for prev, nxt in zip(changes, changes[1:]):
            if nxt.start.nanos < prev.end.nanos:
                raise ValueError(f"overlapping change spans: {prev!r} vs {nxt!r}")

        old_ids = {id(s) for s in self._segments}
        segs = self._segments
        n = len(segs)
        i = 0
        out: list = []
        pending = None  # tail of an old segment cut by the previous change
        for c in changes:
            deferred = None
            if pending is not None:
                if c.start.nanos >= pending.end.nanos:
                    out.append(pending)
                else:
                    if c.start.nanos > pending.start.nanos:
                        out.append(Segment(pending.start, c.start, pending.entry))
                    if pending.end.nanos > c.end.nanos:
                        deferred = Segment(c.end, pending.end, pending.entry)
                pending = None
            while i < n and segs[i].end.nanos <= c.start.nanos:
                out.append(segs[i])  # untouched: shared node
                i += 1
            if i < n and segs[i].start.nanos < c.start.nanos:
                out.append(Segment(segs[i].start, c.start, segs[i].entry))
            while i < n and segs[i].end.nanos <= c.end.nanos:
                i += 1
            if i < n and segs[i].start.nanos < c.end.nanos:
                pending = Segment(c.end, segs[i].end, segs[i].entry)
                i += 1
            if c.after is not None:
                out.append(Segment(c.start, c.end, c.after))
            if deferred is not None:
                out.append(deferred)
        if pending is not None:
            out.append(pending)
        while i < n:
            out.append(segs[i])  # untouched: shared node
            i += 1

        merged = _merge_adjacent(out)
        result = Timeline._from_normalized(tuple(merged))
        copied = sum(1 for s in merged if id(s) not in old_ids)
        return result, ApplyStats(copied_nodes=copied, shared_nodes=len(merged) - copied)
