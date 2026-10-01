"""Common refinement of several overlapping local calendar partitions."""
from __future__ import annotations

from bisect import bisect_right

from .exact import as_fraction, utc_seconds
from .partition import iter_segments


class CalendarSpec:
    """One local calendar: an offset table plus a boundary grid."""

    __slots__ = ("table", "grid")

    def __init__(self, table, grid):
        self.table = table
        self.grid = grid


class RefinedSegment:
    """An atomic UTC piece carrying every calendar's label."""

    __slots__ = ("start", "end", "labels")

    def __init__(self, start, end, labels):
        self.start = as_fraction(start)
        self.end = as_fraction(end)
        self.labels = dict(labels)

    @property
    def duration(self):
        return self.end - self.start

    def __repr__(self):
        return f"RefinedSegment({self.start}, {self.end}, {self.labels!r})"


class Refinement:
    """Result of ``common_refinement``.

    ``segments`` are disjoint and cover the window exactly. ``preimages``
    maps ``calendar name -> label -> [(utc_start, utc_end), ...]``; a label
    may own several disjoint UTC intervals.
    """

    def __init__(self, window, segments, preimages):
        self.window = (utc_seconds(window[0]), utc_seconds(window[1]))
        self.segments = segments
        self.preimages = preimages

    def preimage_of(self, calendar, label):
        return self.preimages.get(calendar, {}).get(label, [])


def _label_at(segments, starts, t):
    i = bisect_right(starts, t) - 1
    if i < 0 or not (segments[i].start <= t < segments[i].end):
        raise ValueError("no segment covers the given instant")
    return segments[i].label


def common_refinement(window, calendars):
    """Build the common refinement of the given calendars over the window.

    ``calendars`` maps a name to a ``CalendarSpec``. Every atomic segment of
    the refinement reports the label each calendar assigns to it, and the
    per-label UTC preimages (possibly several disjoint intervals) are
    collected alongside.
    """
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    per_calendar = {}
    for name, spec in calendars.items():
        segs = iter_segments((start, end), spec.table, spec.grid)
        per_calendar[name] = sorted(segs, key=lambda s: (s.start, s.end))
    cuts = {start, end}
    for segs in per_calendar.values():
        for s in segs:
            cuts.add(s.start)
            cuts.add(s.end)
    ordered = sorted(cuts)
    starts_by_cal = {name: [s.start for s in segs] for name, segs in per_calendar.items()}
    refined = []
    for a, b in zip(ordered, ordered[1:]):
        if a >= b:
            continue
        labels = {
            name: _label_at(segs, starts_by_cal[name], a)
            for name, segs in per_calendar.items()
        }
        refined.append(RefinedSegment(a, b, labels))
    preimages = {}
    for name, segs in per_calendar.items():
        index = {}
        for s in segs:
            index.setdefault(s.label, []).append((s.start, s.end))
        preimages[name] = index
    return Refinement((start, end), refined, preimages)
