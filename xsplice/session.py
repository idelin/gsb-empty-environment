"""Incremental editing sessions.

Because every segment owns an independent gain envelope (the global curve
is a plain sum, never a per-pair rescale), editing one segment can only
change the output inside that segment's own old and new spans.  A
:class:`Revision` reports exactly those dirty output intervals plus the
affected source intervals, so callers can re-render just what changed.
"""

from __future__ import annotations

from dataclasses import dataclass

from .plan import Plan, compile_plan
from .segment import Segment


@dataclass(frozen=True)
class Revision:
    """Result of one edit.

    changed_output: merged (start, end) output sample ranges to re-render
    changed_source: (src_id, lo, hi) source ranges touched by the edit
    plan: the newly compiled plan
    """

    plan: Plan
    changed_output: tuple
    changed_source: tuple


class Session:
    """Holds the current segment set and recompiles incrementally."""

    def __init__(self, sources, sample_rate, **compile_options):
        self._sources = sources
        self._sample_rate = sample_rate
        self._options = compile_options
        self._segments: dict[str, Segment] = {}
        self._plan = self._compile(self._segments)

    @property
    def plan(self) -> Plan:
        return self._plan

    def upsert(self, segment: Segment) -> Revision:
        """Insert or replace a segment; returns the incremental Revision.

        If compilation fails (validation, short-segment reject, gain
        constraint) the session is left untouched.
        """
        segments = dict(self._segments)
        segments[segment.seg_id] = segment
        plan = self._compile(segments)
        old = self._segments.get(segment.seg_id)
        self._segments = segments
        self._plan = plan
        return Revision(
            plan=plan,
            changed_output=_output_spans(old, segment),
            changed_source=_source_spans(old, segment),
        )

    def remove(self, seg_id: str) -> Revision:
        """Remove a segment; returns the incremental Revision."""
        if seg_id not in self._segments:
            raise KeyError(f"unknown segment id: {seg_id!r}")
        segments = dict(self._segments)
        old = segments.pop(seg_id)
        plan = self._compile(segments)
        self._segments = segments
        self._plan = plan
        return Revision(
            plan=plan,
            changed_output=_output_spans(old, None),
            changed_source=_source_spans(old, None),
        )

    def _compile(self, segments) -> Plan:
        return compile_plan(
            list(segments.values()),
            self._sources,
            self._sample_rate,
            **self._options,
        )


def _output_spans(old: Segment | None, new: Segment | None) -> tuple:
    spans = []
    for seg in (old, new):
        if seg is not None and not seg.is_empty:
            spans.append((seg.offset, seg.end))
    return tuple(_merge(spans))


def _source_spans(old: Segment | None, new: Segment | None) -> tuple:
    spans = []
    for seg in (old, new):
        if seg is not None and not seg.is_empty:
            entry = (seg.src_id, seg.src_lo, seg.src_hi)
            if entry not in spans:
                spans.append(entry)
    return tuple(spans)


def _merge(spans):
    """Merge overlapping or adjacent (start, end) spans."""
    merged = []
    for start, end in sorted(spans):
        if merged and start <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], end))
        else:
            merged.append((start, end))
    return merged
