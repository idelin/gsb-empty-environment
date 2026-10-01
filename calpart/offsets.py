"""Synthetic UTC-offset transition tables.

An :class:`OffsetTable` is a piecewise-constant function
``offset(t) : Instant -> seconds`` built from a base offset plus a sorted
list of transitions.  It supports:

* instant -> wall-clock conversion (:meth:`OffsetTable.instant_to_wall`);
* wall-clock -> *all* instant preimages (:meth:`OffsetTable.wall_to_instants`),
  which may be empty (the wall time was skipped), a single instant, or
  several instants (the wall time occurred more than once);
* partial revision of the transition list over a bounded range
  (:meth:`OffsetTable.revise`) that provably leaves conversions outside the
  range unchanged;
* lazy, page-wise iteration over transitions
  (:meth:`OffsetTable.iter_transition_pages`).
"""

from __future__ import annotations

from bisect import bisect_left, bisect_right
from fractions import Fraction
from itertools import islice
from typing import Iterator, Optional, Tuple

from .exact import WallTime, as_fraction


class Transition:
    """At UTC instant ``at`` the offset becomes ``offset`` seconds."""

    __slots__ = ("at", "offset")

    def __init__(self, at, offset):
        self.at = as_fraction(at)
        self.offset = as_fraction(offset)

    def __eq__(self, other):
        if isinstance(other, Transition):
            return self.at == other.at and self.offset == other.offset
        return NotImplemented

    def __hash__(self):
        return hash((Transition, self.at, self.offset))

    def __repr__(self):
        return f"Transition(at={self.at}, offset={self.offset})"


class OffsetTable:
    """Piecewise-constant UTC offset function built from a transition list."""

    def __init__(self, base_offset, transitions=()):
        self.base_offset = as_fraction(base_offset)
        merged = {}
        for tr in transitions:
            if not isinstance(tr, Transition):
                tr = Transition(*tr)
            merged[tr.at] = tr.offset  # same instant: last one wins
        self._ats = sorted(merged)
        self._offsets = [merged[a] for a in self._ats]
        self.transitions = tuple(Transition(a, merged[a]) for a in self._ats)

    # -- queries ---------------------------------------------------------

    def offset_bounds(self) -> Tuple[Fraction, Fraction]:
        """(min, max) offset ever taken by this table."""
        offs = self._offsets + [self.base_offset]
        return min(offs), max(offs)

    def offset_at(self, t) -> Fraction:
        t = as_fraction(t)
        i = bisect_right(self._ats, t) - 1
        return self.base_offset if i < 0 else self._offsets[i]

    def transitions_between(self, start, end) -> Tuple[Transition, ...]:
        """All transitions with ``start <= at < end``."""
        lo = bisect_left(self._ats, as_fraction(start))
        hi = bisect_left(self._ats, as_fraction(end))
        return self.transitions[lo:hi]

    def iter_transition_pages(self, start, end, page_size: int) -> Iterator[Tuple[Transition, ...]]:
        """Lazily yield pages of at most ``page_size`` transitions in [start, end)."""
        if page_size <= 0:
            raise ValueError("page_size must be positive")
        lo = bisect_left(self._ats, as_fraction(start))
        hi = bisect_left(self._ats, as_fraction(end))
        page = []
        for tr in islice(self.transitions, lo, hi):
            page.append(tr)
            if len(page) == page_size:
                yield tuple(page)
                page = []
        if page:
            yield tuple(page)

    # -- revision --------------------------------------------------------

    def revise(self, start, end, new_transitions) -> "OffsetTable":
        """Return a new table where transitions inside [start, end) are replaced.

        ``new_transitions`` are clipped to the range.  A restoring transition
        is inserted at ``end`` carrying the offset the *original* table had at
        ``end``, so conversions at every instant outside [start, end) are
        identical to the original table.
        """
        start = as_fraction(start)
        end = as_fraction(end)
        if start > end:
            raise ValueError("start must not exceed end")
        merged = {}

        def put(at, offset, prio):
            if at not in merged or merged[at][0] <= prio:
                merged[at] = (prio, offset)

        for tr in self.transitions:
            if tr.at < start or tr.at >= end:
                put(tr.at, tr.offset, 0)
        for tr in new_transitions:
            if not isinstance(tr, Transition):
                tr = Transition(*tr)
            if start <= tr.at < end:
                put(tr.at, tr.offset, 1)
        put(end, self.offset_at(end), 2)  # restore original behaviour at end
        out = [Transition(at, off) for at, (_, off) in sorted(merged.items())]
        return OffsetTable(self.base_offset, out)

    # -- conversions -----------------------------------------------------

    def instant_to_wall(self, t) -> WallTime:
        t = as_fraction(t)
        return WallTime.from_naive_seconds(t + self.offset_at(t))

    def wall_to_instants(self, wall: WallTime) -> Tuple[Fraction, ...]:
        """All UTC instants (sorted) whose local reading equals ``wall``.

        The result may be empty (skipped wall time), have one element, or
        several (a wall time that occurred more than once).
        """
        w = wall.to_naive_seconds()
        lo_off, hi_off = self.offset_bounds()
        lo_t = w - hi_off
        hi_t = w - lo_off
        lo = bisect_left(self._ats, lo_t)
        hi = bisect_right(self._ats, hi_t)
        results = []
        for i in range(lo - 1, hi):
            off = self.base_offset if i < 0 else self._offsets[i]
            seg_start: Optional[Fraction] = self._ats[i] if i >= 0 else None
            seg_end: Optional[Fraction] = self._ats[i + 1] if i + 1 < len(self._ats) else None
            t = w - off
            if seg_start is not None and t < seg_start:
                continue
            if seg_end is not None and t >= seg_end:
                continue
            results.append(t)
        return tuple(sorted(set(results)))
