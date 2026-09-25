"""Exhaustive-selection oracle used to cross-check the sweep solver."""
from __future__ import annotations

from itertools import product

from .model import common_interval, is_consistent, visible_versions


def merge_intervals(intervals) -> list:
    """Union of half-open intervals; touching intervals merge."""
    merged: list[list] = []
    for lo, hi in sorted(intervals):
        if merged and lo <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], hi)
        else:
            merged.append([lo, hi])
    return [tuple(m) for m in merged]


def exhaustive_feasible_intervals(facts, rules, query) -> list:
    """Enumerate every complete selection, keep consistent ones, and union
    their common intervals. Reference implementation for small models."""
    visible = visible_versions(facts, query.observation_time)
    per_dim = []
    for dim in query.dimensions:
        cands = [f for f in visible.values() if f.dimension == dim]
        if not cands:
            return []
        per_dim.append(cands)
    intervals = []
    for combo in product(*per_dim):
        if is_consistent(combo, rules):
            interval = common_interval(combo)
            if interval is not None:
                intervals.append(interval)
    return merge_intervals(intervals)
