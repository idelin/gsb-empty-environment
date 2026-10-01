"""Partition a UTC window into local calendar periods.

Each segment carries exact UTC endpoints and its true elapsed duration; a
single local period may contribute several disjoint UTC segments when the
offset table makes local time repeat.
"""
from __future__ import annotations

from fractions import Fraction

from .exact import as_fraction, utc_seconds


class Segment:
    """One UTC piece of a local calendar period."""

    __slots__ = ("start", "end", "label")

    def __init__(self, start, end, label):
        self.start = as_fraction(start)
        self.end = as_fraction(end)
        self.label = label

    @property
    def duration(self) -> Fraction:
        return self.end - self.start

    def __eq__(self, other):
        return (
            isinstance(other, Segment)
            and (self.start, self.end, self.label) == (other.start, other.end, other.label)
        )

    def __repr__(self):
        return f"Segment({self.start}, {self.end}, {self.label!r})"


def local_range_attained(window, table):
    """The [min, max) local readings actually attained inside the UTC window."""
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    cuts = [start, *[t for t, _ in table.transitions_between(start, end)], end]
    lows, highs = [], []
    for a, b in zip(cuts, cuts[1:]):
        off = table.offset_at(a)
        lows.append(a + off)
        highs.append(b + off)
    return min(lows), max(highs)


def iter_segments(window, table, grid):
    """Lazily yield ``Segment`` objects covering ``[start, end)`` exactly.

    The generator walks one local period at a time (never one second at a
    time), so it composes with lazy pagination over long windows.
    """
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    if end <= start:
        return
    lo, hi = local_range_attained(window, table)
    boundary = grid.floor(lo)
    while boundary < hi:
        nxt = grid.next_boundary(boundary)
        label = grid.label(boundary)
        for a, b in table.preimage_local_interval(boundary, nxt):
            s = max(a, start)
            e = min(b, end)
            if s < e:
                yield Segment(s, e, label)
        boundary = nxt


def partition(window, table, grid):
    """Eagerly materialise ``iter_segments`` into a list."""
    return list(iter_segments(window, table, grid))


class SegmentPager:
    """Lazy pagination over a segment iterator: pages are pulled on demand."""

    def __init__(self, segments, page_size):
        if page_size <= 0:
            raise ValueError("page_size must be positive")
        self._it = iter(segments)
        self.page_size = page_size
        self.exhausted = False
        self.pages_served = 0

    def next_page(self):
        if self.exhausted:
            return []
        page = []
        while len(page) < self.page_size:
            try:
                page.append(next(self._it))
            except StopIteration:
                self.exhausted = True
                break
        if page:
            self.pages_served += 1
        return page

    def __iter__(self):
        while True:
            page = self.next_page()
            if not page:
                return
            yield page
