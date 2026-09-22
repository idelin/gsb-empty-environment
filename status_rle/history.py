"""Run-length encoded status history with source indexing.

Editing (insert / retract / correct) returns a :class:`Patch` describing
exactly which segments disappeared and which appeared, so downstream
consumers can maintain a replica without re-reading the full state.
"""

from __future__ import annotations

from itertools import groupby
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

from .model import Event, Patch, Segment


def build_segments(events: Iterable[Event]) -> List[Segment]:
    """Recompute the full RLE segment list from raw events.

    This is the ground-truth function: query results and patch-replayed
    results are both validated against it.
    """
    ordered = sorted(events)  # canonical (time, source, seq)
    segments: List[Segment] = []
    for t, group in groupby(ordered, key=lambda e: e.time):
        at_t = tuple(group)
        effective = at_t[-1].status  # last in canonical order wins
        if segments and segments[-1].status == effective:
            prev = segments[-1]
            segments[-1] = Segment(
                status=prev.status,
                start=prev.start,
                end=None,  # fixed up below
                provenance=prev.provenance + at_t,
            )
        else:
            segments.append(
                Segment(status=effective, start=t, end=None, provenance=at_t)
            )
    for i in range(len(segments) - 1):
        seg = segments[i]
        segments[i] = Segment(seg.status, seg.start, segments[i + 1].start,
                              seg.provenance)
    return segments


def diff_segments(old: Sequence[Segment], new: Sequence[Segment]) -> Patch:
    """Value-based delta: segments only in ``old`` vs only in ``new``."""
    new_set = set(new)
    old_set = set(old)
    removed = tuple(s for s in old if s not in new_set)
    added = tuple(s for s in new if s not in old_set)
    return Patch(removed=removed, added=added)


def apply_patch(segments: Sequence[Segment], patch: Patch) -> List[Segment]:
    """Apply ``patch`` to a segment snapshot; returns the new snapshot.

    Raises ``ValueError`` if a removed segment is not present (stale or
    mis-ordered patch) or if the result would overlap.
    """
    remaining = list(segments)
    for seg in patch.removed:
        try:
            remaining.remove(seg)
        except ValueError:
            raise ValueError(f"stale patch: segment not found: {seg}") from None
    result = sorted(remaining + list(patch.added),
                    key=lambda s: (s.start, s.status, s.provenance))
    for left, right in zip(result, result[1:]):
        if left.end is None or left.end > right.start:
            raise ValueError(f"overlapping segments after patch: {left} {right}")
    return result


class StatusHistory:
    """Editable event store exposing RLE queries and patch-producing edits."""

    def __init__(self, events: Iterable[Event] = ()):
        self._events: Dict[Tuple[str, int], Event] = {}
        self._segments: List[Segment] = []
        for e in events:
            self._store(e)
        self._segments = build_segments(self._events.values())

    # ----------------------------------------------------------- edits
    def insert(self, source: str, seq: int, time: int, status: str) -> Patch:
        """Add a new record.  Identity ``(source, seq)`` must be unused."""
        event = Event(time=time, source=source, seq=seq, status=status)
        if event.ref in self._events:
            raise ValueError(f"duplicate event identity {event.ref}; "
                             f"use correct() to change payload")
        self._store(event)
        return self._commit()

    def retract(self, source: str, seq: int) -> Patch:
        """Withdraw the record identified by ``(source, seq)``."""
        if (source, seq) not in self._events:
            raise KeyError(f"no event with identity {(source, seq)}")
        del self._events[(source, seq)]
        return self._commit()

    def correct(self, source: str, seq: int, *,
                time: Optional[int] = None,
                status: Optional[str] = None) -> Patch:
        """Fix payload of an existing record; one atomic patch is emitted."""
        ref = (source, seq)
        if ref not in self._events:
            raise KeyError(f"no event with identity {ref}")
        old = self._events[ref]
        self._events[ref] = Event(
            time=old.time if time is None else time,
            source=source,
            seq=seq,
            status=old.status if status is None else status,
        )
        return self._commit()

    # ---------------------------------------------------------- queries
    def segments(self) -> List[Segment]:
        """Current RLE segments, each carrying its source index."""
        return list(self._segments)

    def segment_at(self, t: int) -> Optional[Segment]:
        """Segment covering integer time ``t`` (None before the first)."""
        for seg in self._segments:
            if seg.covers(t):
                return seg
        return None

    def status_at(self, t: int) -> Optional[str]:
        seg = self.segment_at(t)
        return seg.status if seg else None

    def events_between(self, start: int, end: Optional[int]) -> List[Event]:
        """All raw records with ``start <= time < end`` (end=None: +inf)."""
        return sorted(e for e in self._events.values()
                      if start <= e.time and (end is None or e.time < end))

    def explain(self, t: int) -> Optional[Tuple[Segment, Event]]:
        """(segment, effective event) answering 'why this status at t'."""
        seg = self.segment_at(t)
        return (seg, seg.effective_event) if seg else None

    # ---------------------------------------------------------- intern
    def _store(self, event: Event) -> None:
        self._events[event.ref] = event

    def _commit(self) -> Patch:
        new = build_segments(self._events.values())
        patch = diff_segments(self._segments, new)
        self._segments = new
        return patch
