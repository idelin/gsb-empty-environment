"""Coverage-conservation checks.

Two complementary strategies:

- ``verify_piecewise_conservation`` cuts the window at every offset
  transition and checks each constant-offset piece exactly (the reference
  verification: wall-clock length equals UTC length on every piece).
- ``jump_scan`` verifies possibly enormous windows in O(#transitions) by
  jumping whole calendar periods arithmetically, recording the jump work in
  ``WorkStats`` instead of scanning second by second.
"""
from __future__ import annotations

from fractions import Fraction

from .exact import utc_seconds


class WorkStats:
    """Bookkeeping for jump-based verification of very long windows."""

    __slots__ = ("pieces_checked", "transitions_scanned", "bulk_jumps", "periods_jumped")

    def __init__(self):
        self.pieces_checked = 0
        self.transitions_scanned = 0
        self.bulk_jumps = 0
        self.periods_jumped = 0

    def __repr__(self):
        return (
            f"WorkStats(pieces_checked={self.pieces_checked}, "
            f"transitions_scanned={self.transitions_scanned}, "
            f"bulk_jumps={self.bulk_jumps}, "
            f"periods_jumped={self.periods_jumped})"
        )


def cut_at_transitions(window, table):
    """Cut the UTC window at every offset transition (reference cutting)."""
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    pts = [start, *[t for t, _ in table.transitions_between(start, end)], end]
    return [(a, b) for a, b in zip(pts, pts[1:]) if a < b]


def verify_piecewise_conservation(window, table, stats=None):
    """Cut at every transition; on each piece the offset is constant, so the
    wall-clock length must equal the UTC length, and the pieces must sum to
    the window length exactly."""
    stats = stats or WorkStats()
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    pieces = cut_at_transitions(window, table)
    total = Fraction(0)
    for a, b in pieces:
        off = table.offset_at(a)
        wall_len = (b + off) - (a + off)
        assert wall_len == b - a, "wall-clock length must equal UTC length on a constant-offset piece"
        total += b - a
    stats.pieces_checked += len(pieces)
    stats.transitions_scanned += max(0, len(pieces) - 1)
    assert total == end - start, "pieces must exactly cover the window"
    return total, stats


def verify_segments_cover(segments, window):
    """Segments must be disjoint, contiguous and their union exactly the
    window (no gaps, no overlaps), all with exact endpoint arithmetic."""
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    segs = sorted(segments, key=lambda s: (s.start, s.end))
    assert segs, "empty partition"
    assert segs[0].start == start, f"first segment starts at {segs[0].start}, window at {start}"
    assert segs[-1].end == end, f"last segment ends at {segs[-1].end}, window at {end}"
    for prev, nxt in zip(segs, segs[1:]):
        assert prev.end == nxt.start, f"gap or overlap between {prev} and {nxt}"
    total = Fraction(0)
    for s in segs:
        assert s.start < s.end, f"degenerate segment {s}"
        total += s.duration
    assert total == end - start, "segment durations must sum to the window length"
    return total


def jump_scan(window, table, grid, stats=None):
    """Verify coverage of a possibly enormous window without walking periods.

    For each constant-offset stretch the local axis is affine in UTC, so the
    number of calendar periods touched is computed arithmetically (one bulk
    jump per stretch) and recorded, rather than scanning period by period.
    """
    stats = stats or WorkStats()
    start, end = utc_seconds(window[0]), utc_seconds(window[1])
    pieces = cut_at_transitions(window, table)
    total = Fraction(0)
    for a, b in pieces:
        off = table.offset_at(a)
        l0, l1 = a + off, b + off
        first = grid.floor(l0)
        last = grid.floor(l1)
        touched = grid.count_between(first, last)
        if not grid.is_boundary(l1):
            touched += 1
        stats.periods_jumped += touched
        stats.bulk_jumps += 1
        total += b - a
    stats.pieces_checked += len(pieces)
    stats.transitions_scanned += max(0, len(pieces) - 1)
    assert total == end - start, "stretches must exactly cover the window"
    return total, stats
