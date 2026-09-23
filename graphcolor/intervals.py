"""Interval overlap graphs with half-open [start, end) semantics.

Two intervals conflict iff they properly overlap; touching endpoints
(one ends exactly where the other starts) do not conflict.
"""

from __future__ import annotations

from .graph import Graph


def interval_overlap_graph(intervals: dict[str, tuple[float, float]]) -> Graph:
    """Build a graph whose edges join properly-overlapping half-open intervals."""
    g = Graph()
    items = sorted(intervals.items())
    for vid, (start, end) in items:
        if not start < end:
            raise ValueError(f"interval {vid!r} must satisfy start < end")
        g.add_vertex(vid)
    for i, (a, (s1, e1)) in enumerate(items):
        for b, (s2, e2) in items[i + 1:]:
            if s1 < e2 and s2 < e1:
                g.add_edge(f"overlap[{a}|{b}]", a, b)
    return g
