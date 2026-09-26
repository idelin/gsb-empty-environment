"""Bitemporal rectangle index.

Indexes rectangles of the form ``[version_from, version_to) x [valid_from,
valid_to)`` where ``version_to``/``valid_to`` may be ``None`` (open ended).

The valid-time axis is cut into elementary, contiguous, non-overlapping
segments.  Each segment carries an append-only list of version entries that
tiles the version axis ``[0, +inf)``.  A rectangle is therefore one
``(segment, version entry)`` pair.

Queries never scan all history:

* point query  = bisect the segment cuts, then bisect the segment's
  version-entry list  (O(log S + log H));
* range query  = bisect to the first intersecting segment/entry, then walk
  only the intersecting rectangles.

The index exposes a ``probes`` counter (segments/entries actually touched)
so tests can assert that queries stay sub-linear in the history size.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from typing import Any, Iterator, Optional

from .timekit import TimePoint


@dataclass(frozen=True)
class Entry:
    """One version run of a segment: payload valid for [version_from, version_to)."""

    version_from: int
    version_to: Optional[int]  # None = still current (open)
    payload: Any


@dataclass(frozen=True)
class Rectangle:
    """A rectangle clipped to a query box, as returned by range queries."""

    valid_from: Optional[TimePoint]   # None = -infinity
    valid_to: Optional[TimePoint]     # None = +infinity (open ended)
    version_from: int
    version_to: Optional[int]         # None = still current (open)
    payload: Any


@dataclass
class _Segment:
    lo: Optional[TimePoint]   # None = -infinity (only the first segment)
    hi: Optional[TimePoint]   # None = +infinity (only the last segment)
    entries: list[Entry]      # tiles versions [0, +inf); only the last is open
    vfroms: list[int]         # parallel list of entry.version_from for bisect


def _max_lo(a, b):
    if a is None:
        return b
    if b is None:
        return a
    return max(a, b)


def _min_hi(a, b):
    if a is None:
        return b
    if b is None:
        return a
    return min(a, b)


class RectangleIndex:
    def __init__(self, empty_payload=None):
        self._empty = empty_payload
        self._segments: list[_Segment] = [
            _Segment(None, None, [Entry(0, None, empty_payload)], [0])
        ]
        self._cuts: list[TimePoint] = []  # cuts[k] == segments[k + 1].lo
        self.probes = 0

    # -- introspection -----------------------------------------------------

    def reset_probes(self) -> None:
        self.probes = 0

    def rectangle_count(self) -> int:
        return sum(len(s.entries) for s in self._segments)

    def segment_count(self) -> int:
        return len(self._segments)

    def segments(self) -> Iterator[tuple[Optional[TimePoint], Optional[TimePoint], list[Entry]]]:
        for s in self._segments:
            yield s.lo, s.hi, list(s.entries)

    # -- internal navigation -------------------------------------------------

    def _locate(self, tp: TimePoint) -> int:
        """Index of the segment containing ``tp`` (half-open: a point exactly
        on a cut belongs to the segment *starting* there, never to both)."""
        return bisect_right(self._cuts, tp)

    # -- mutation (used by the store, all inside one atomic transaction) -----

    def split_at(self, tp: TimePoint) -> None:
        """Cut the segment containing ``tp`` in two (no-op if already a cut)."""
        i = self._locate(tp)
        seg = self._segments[i]
        if seg.lo == tp:
            return
        new = _Segment(tp, seg.hi, list(seg.entries), list(seg.vfroms))
        seg.hi = tp
        self._segments.insert(i + 1, new)
        self._cuts.insert(i, tp)

    def set_payload(self, seg_index: int, version: int, payload) -> bool:
        """Close the current rectangle of the segment at ``version`` and open a
        new one with ``payload``.  Returns False (and changes nothing) when the
        payload is unchanged, so untouched rectangles are never split."""
        seg = self._segments[seg_index]
        last = seg.entries[-1]
        assert last.version_to is None, "current entry must be open"
        if last.payload == payload:
            return False
        seg.entries[-1] = Entry(last.version_from, version, last.payload)
        seg.entries.append(Entry(version, None, payload))
        seg.vfroms.append(version)
        return True

    def try_merge(self, seg_index: int) -> bool:
        """Merge segments ``seg_index`` and ``seg_index + 1`` when their entire
        version histories are identical (keeps the canonical form minimal)."""
        if seg_index + 1 >= len(self._segments):
            return False
        a, b = self._segments[seg_index], self._segments[seg_index + 1]
        if a.entries != b.entries:
            return False
        a.hi = b.hi
        del self._segments[seg_index + 1]
        del self._cuts[seg_index]
        return True

    # -- queries ---------------------------------------------------------------

    def point_query(self, version: int, tp: TimePoint) -> tuple[Entry, Optional[TimePoint], Optional[TimePoint]]:
        """The (entry, segment_lo, segment_hi) visible at (version, tp)."""
        i = self._locate(tp)
        seg = self._segments[i]
        j = bisect_right(seg.vfroms, version) - 1
        entry = seg.entries[j]
        assert entry.version_from <= version
        assert entry.version_to is None or version < entry.version_to
        self.probes += 2  # one segment lookup + one entry lookup
        return entry, seg.lo, seg.hi

    def range_query(
        self,
        version_from: int,
        version_to: Optional[int],
        valid_from: Optional[TimePoint],
        valid_to: Optional[TimePoint],
    ) -> Iterator[Rectangle]:
        """All rectangles intersecting [version_from, version_to) x
        [valid_from, valid_to), clipped to the box.  ``None`` = open bound."""
        i = 0 if valid_from is None else self._locate(valid_from)
        while i < len(self._segments):
            seg = self._segments[i]
            if valid_to is not None and seg.lo is not None and seg.lo >= valid_to:
                break
            self.probes += 1
            j = max(0, bisect_right(seg.vfroms, version_from) - 1)
            while j < len(seg.entries):
                entry = seg.entries[j]
                if version_to is not None and entry.version_from >= version_to:
                    break
                self.probes += 1
                yield Rectangle(
                    valid_from=_max_lo(seg.lo, valid_from),
                    valid_to=_min_hi(seg.hi, valid_to),
                    version_from=max(entry.version_from, version_from),
                    version_to=_min_hi(entry.version_to, version_to),
                    payload=entry.payload,
                )
                j += 1
            i += 1
