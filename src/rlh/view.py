"""Patch-consumer view: reconstruct segments by replaying a patch log.

This is the independent second query path. A consumer that only ever received
the initial segments plus the stream of :class:`Patch` objects ends up with
exactly the same segments as ``HistoryStore.segments()``.
"""

from __future__ import annotations

from collections.abc import Iterable as IterableABC
from typing import Iterable, Optional

from .model import Patch, Segment


class SegmentedView:
    def __init__(self, initial: Iterable[Segment] = ()) -> None:
        self._segments: list[Segment] = list(initial)

    def apply(self, patch: Patch) -> "SegmentedView":
        self._segments = patch.apply_to(self._segments)
        return self

    def apply_all(self, patches: Iterable[Patch]) -> "SegmentedView":
        for patch in patches:
            self.apply(patch)
        return self

    def segments(self) -> tuple[Segment, ...]:
        return tuple(self._segments)

    def segment_at(self, t: int) -> Optional[Segment]:
        for segment in self._segments:
            if segment.contains(t):
                return segment
        return None

    def trace(self, sid: int) -> tuple[Record]:
        for segment in self._segments:
            if segment.sid == sid:
                return segment.sources
        raise KeyError(f"no live segment with id {sid}")
