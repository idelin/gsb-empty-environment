"""Run-length encoded device-state history with source indexing.

Every event keeps its (source, seq) identity. Consecutive events with the
same state collapse into one Segment over a half-open interval [start, end).
Edits (insert / retract / correct) return incremental Patches of segments so
downstream consumers can maintain a segment view without the raw events.

Ordering / conflict rule
------------------------
Events are canonically ordered by (time, source, seq). When several events
share a timestamp, the one sorting last decides the state at that instant;
earlier same-time events remain as evidence inside the segment that ends at
that timestamp. No event is ever dropped from the source index.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Iterable, Optional


@dataclass(frozen=True, order=True)
class EventRef:
    """Identity and payload of one raw record."""

    source: str
    seq: int
    time: int
    state: str

    @property
    def key(self) -> tuple[str, int]:
        return (self.source, self.seq)


@dataclass(frozen=True)
class Segment:
    """A maximal run of equal state over [start, end); end=None means open."""

    state: str
    start: int
    end: Optional[int]
    members: tuple[EventRef, ...]

    def covers(self, time: int) -> bool:
        return self.start <= time and (self.end is None or time < self.end)


@dataclass(frozen=True)
class Patch:
    """Incremental segment diff produced by one edit."""

    removed: tuple[Segment, ...] = ()
    added: tuple[Segment, ...] = ()

    def is_empty(self) -> bool:
        return not self.removed and not self.added


def build_segments(events: Iterable[EventRef]) -> list[Segment]:
    """Compress raw events into run-length segments (the single source of truth)."""
    ordered = sorted(events, key=lambda e: (e.time, e.source, e.seq))
    segments: list[Segment] = []
    for event in ordered:
        if segments and segments[-1].state == event.state:
            prev = segments[-1]
            segments[-1] = Segment(prev.state, prev.start, prev.end, prev.members + (event,))
        else:
            if segments:
                prev = segments[-1]
                segments[-1] = Segment(prev.state, prev.start, event.time, prev.members)
            segments.append(Segment(event.state, event.time, None, (event,)))
    return segments


def diff_segments(old: list[Segment], new: list[Segment]) -> Patch:
    removed = tuple(seg for seg in old if seg not in new)
    added = tuple(seg for seg in new if seg not in old)
    return Patch(removed=removed, added=added)


def apply_patch(segments: Iterable[Segment], patch: Patch) -> list[Segment]:
    """Apply a patch to a segment view; pure function, returns a new list."""
    removed = set(patch.removed)
    kept = [seg for seg in segments if seg not in removed]
    kept.extend(patch.added)
    # order by start; for equal starts (zero-length segments after same-time
    # conflicts) the bounded segment precedes the open-ended one
    kept.sort(key=lambda seg: (seg.start, seg.end is None, seg.end))
    return kept


class StateHistory:
    """Editable event store exposing RLE segment queries and patch-producing edits."""

    def __init__(self) -> None:
        self._events: dict[tuple[str, int], EventRef] = {}
        self._segments: list[Segment] = []

    # ---- edits -----------------------------------------------------------

    def insert(self, source: str, seq: int, time: int, state: str) -> Patch:
        key = (source, seq)
        if key in self._events:
            raise ValueError(f"duplicate event {key}; use correct() to amend it")
        return self._mutate({key: EventRef(source, seq, time, state)}, delete=())

    def retract(self, source: str, seq: int) -> Patch:
        key = (source, seq)
        if key not in self._events:
            raise KeyError(f"unknown event {key}")
        return self._mutate({}, delete=(key,))

    def correct(
        self,
        source: str,
        seq: int,
        *,
        time: Optional[int] = None,
        state: Optional[str] = None,
    ) -> Patch:
        """Amend an existing event in place; emits a single combined patch."""
        key = (source, seq)
        old = self._events.get(key)
        if old is None:
            raise KeyError(f"unknown event {key}")
        new = EventRef(
            source,
            seq,
            old.time if time is None else time,
            old.state if state is None else state,
        )
        if new == old:
            return Patch()
        return self._mutate({key: new}, delete=())

    def _mutate(
        self,
        upserts: dict[tuple[str, int], EventRef],
        delete: tuple[tuple[str, int], ...],
    ) -> Patch:
        old_segments = self._segments
        for key in delete:
            del self._events[key]
        self._events.update(upserts)
        self._segments = build_segments(self._events.values())
        return diff_segments(old_segments, self._segments)

    # ---- queries ---------------------------------------------------------

    def segments(self) -> list[Segment]:
        return list(self._segments)

    def state_at(self, time: int) -> Optional[str]:
        for seg in self._segments:
            if seg.covers(time):
                return seg.state
        return None

    def provenance(self, time: int) -> tuple[EventRef, ...]:
        """Which raw records formed the segment covering `time`."""
        for seg in self._segments:
            if seg.covers(time):
                return seg.members
        return ()

    def events(self) -> list[EventRef]:
        return sorted(self._events.values(), key=lambda e: (e.time, e.source, e.seq))
