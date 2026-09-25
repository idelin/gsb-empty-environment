"""Rendering of multi-track sources through an edit plan.

Two renderers are provided and must agree:

* ``render_direct``  - a per-sample interpreter: every output sample asks the
  time map for its source position and interpolates each track.
* ``render_chunked`` - applies the fixed plan chunk by chunk; inside a chunk
  it walks segment by segment with an incremental position, which is how a
  streaming engine would consume the plan.

Interpolation policy (explicit):
    linear interpolation between ``floor(pos)`` and ``floor(pos) + 1``.
    The kernel therefore looks at most ONE source sample ahead.
    Positions within ``EDGE_EPSILON`` of an integer are snapped to the
    sample grid first: grid-aligned source positions are exact by
    construction, and snapping keeps tap selection (and therefore the
    missing-data mask) immune to one-ulp differences between renderers.

Latency / lookahead declaration:
    offline renderer, algorithmic latency = 0 output samples;
    interpolation lookahead <= 1 source sample.

Edge / filter-tail policy:
    positions outside ``[0, len(track) - 1]`` are clamped for the value, and
    the output mask marks the sample invalid (the interpolation kernel's tail
    reached past the available source).  Positions within ``EDGE_EPSILON``
    of the boundary still count as inside, so a one-ulp difference between
    the two renderers can never flip the mask.  A missing tap (``None``)
    also marks the sample invalid; the value then holds the last valid tap.

Numeric tolerance:
    chunked vs. direct positions may differ by a few ulps because the
    chunked renderer accumulates ``slope / sample_rate`` per sample; all
    comparisons use ``TOLERANCE = 1e-9``.
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from typing import List, Optional, Sequence, Tuple

from plan import EditPlan
from timemap import Kind

LATENCY_SAMPLES = 0
LOOKAHEAD_SOURCE_SAMPLES = 1
TOLERANCE = 1e-9
EDGE_EPSILON = 1e-6

Track = Sequence[Optional[float]]


@dataclass
class RenderResult:
    values: List[List[float]]   # per track, per output sample
    mask: List[List[bool]]      # per track: True where the value is valid
    kinds: List[Kind]           # per output sample, shared by all tracks
    positions: List[float]      # source position per output sample, shared
    sample_rate: int


def _interp(track: Track, pos: float) -> Tuple[float, bool]:
    """Linear interpolation at fractional ``pos``; see module docstring."""
    n = len(track)
    if n == 0:
        return 0.0, False
    in_range = -EDGE_EPSILON <= pos <= (n - 1) + EDGE_EPSILON
    p = min(max(pos, 0.0), n - 1.0)
    nearest = round(p)
    if abs(p - nearest) <= EDGE_EPSILON:
        p = float(nearest)
    i = int(math.floor(p))
    j = min(i + 1, n - 1)
    frac = p - i
    a, b = track[i], track[j]
    if a is None and b is None:
        return 0.0, False
    if a is None:
        return float(b), False
    if b is None:
        return float(a), False
    value = a + (b - a) * frac
    return value, in_range


def _apply_tracks(
    tracks: Sequence[Track], positions: Sequence[float]
) -> Tuple[List[List[float]], List[List[bool]]]:
    values: List[List[float]] = []
    mask: List[List[bool]] = []
    for track in tracks:
        tv: List[float] = []
        tm: List[bool] = []
        for pos in positions:
            v, ok = _interp(track, pos)
            tv.append(v)
            tm.append(ok)
        values.append(tv)
        mask.append(tm)
    return values, mask


def render_direct(
    plan: EditPlan,
    tracks: Sequence[Track],
    sample_rate: int,
    num_output: int,
) -> RenderResult:
    """Per-sample interpreter: the ground-truth renderer."""
    tm = plan.time_map
    positions = [tm.source_at(i / sample_rate) for i in range(num_output)]
    kinds = [tm.kind_at(i / sample_rate) for i in range(num_output)]
    values, mask = _apply_tracks(tracks, positions)
    return RenderResult(values, mask, kinds, positions, sample_rate)


def render_chunked(
    plan: EditPlan,
    tracks: Sequence[Track],
    sample_rate: int,
    num_output: int,
    chunk_size: int = 1024,
) -> RenderResult:
    """Chunked renderer applying the fixed plan segment by segment.

    Each output-time boundary belongs to the following segment, so turning
    points are emitted exactly once regardless of the chunk size.
    """
    if chunk_size <= 0:
        raise ValueError("chunk_size must be positive")
    tm = plan.time_map
    positions = [0.0] * num_output
    kinds: List[Kind] = [Kind.FORWARD] * num_output
    for c0 in range(0, num_output, chunk_size):
        c1 = min(c0 + chunk_size, num_output)
        i = c0
        while i < c1:
            seg = tm.segments[tm.segment_index(i / sample_rate)]
            # First output sample index NOT in this segment (boundary is
            # owned by the next segment), clamped to the chunk end.
            seg_end = min(c1, math.ceil(seg.out_end * sample_rate - 1e-9))
            seg_end = max(seg_end, i + 1)
            pos0 = seg.source_at(i / sample_rate)
            step = seg.slope / sample_rate
            for idx in range(i, seg_end):
                positions[idx] = pos0 + (idx - i) * step
                kinds[idx] = seg.kind
            i = seg_end
    values, mask = _apply_tracks(tracks, positions)
    return RenderResult(values, mask, kinds, positions, sample_rate)
