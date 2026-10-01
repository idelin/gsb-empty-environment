"""Composition of projections across streams (multi-layer mapping).

``compose(query, A, B)`` maps a time interval to indices of stream A, maps
each resulting slice back to the interval it actually covers, and projects
those intervals into stream B.  Because every step is exact, the composed
result equals the direct projection of the (rounding-adjusted) query into
B -- verified by the test-suite.
"""
from __future__ import annotations

from .projection import _project_parts, assemble, project
from .types import Interval


def _merge_runs(runs):
    merged = []
    for lo, hi in sorted(runs):
        if merged and lo <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], hi)
        else:
            merged.append([lo, hi])
    return merged


def project_slices(result, blocks):
    """Project the intervals actually covered by ``result`` into ``blocks``."""
    block_map = {(b.epoch, b.version): b for b in blocks}
    runs, missing, evidence, coverages = {}, [], [], []
    for s in result.slices:
        source = result.blocks[s.key]
        covered = Interval(source.timestamp(s.start_index),
                           source.timestamp(s.end_index - 1))
        _, r, m, ev, covs = _project_parts(covered, block_map.values())
        for key, rl in r.items():
            runs.setdefault(key, []).extend(rl)
        missing.extend(m)
        evidence.extend(ev)
        coverages.extend(covs)
    runs = {k: _merge_runs(v) for k, v in runs.items()}
    return assemble(result.query, block_map, runs, missing, evidence, coverages)


def compose(query, blocks_a, blocks_b):
    """time -> A-indices -> covered time -> B-indices."""
    return project_slices(project(query, blocks_a), blocks_b)
