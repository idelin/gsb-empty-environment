"""Exact scanline fill core.

Fill convention (fixed once and for all -- no epsilon anywhere):

* Pixel (px, py) is sampled at its centre (ox+px+1/2, oy+py+1/2) in
  world coordinates, where (ox, oy) is the world position of pixel
  (0, 0)'s top-left corner.
* A sample point P is classified by casting a ray from P towards +x.
  An edge crosses the ray iff P.y lies in the half-open interval
  [min(y0, y1), max(y0, y1)) and the crossing's x is *strictly*
  greater than P.x.
* Horizontal edges and zero-length edges never cross a ray.

Because every coordinate and every intersection is an exact
fractions.Fraction, points lying exactly on an edge or a vertex are
resolved by the convention itself: the classification equals that of
P + (ex, ey) for infinitesimal ex, ey > 0.  Two polygons sharing an
edge therefore partition the boundary samples deterministically --
no cracks and no double-counted coverage.

fill_row_spans produces runs of covered pixels from the sorted exact
crossings; winding_at is an independent brute-force per-point
implementation of the same convention, used as the pixel-by-pixel
reference in the tests.
"""
from __future__ import annotations

from fractions import Fraction
from typing import List, Sequence, Tuple

from .geometry import Point, to_frac
from .polygon import NONZERO

_HALF = Fraction(1, 2)


def _crossings(rings: Sequence[Sequence[Point]], yc: Fraction):
    """All edge/scanline crossings at y == yc, sorted by exact x.

    Returns a list of (x, direction) where direction is +1 for edges
    going towards +y and -1 for edges going towards -y.
    """
    result: List[Tuple[Fraction, int]] = []
    for ring in rings:
        n = len(ring)
        for i in range(n):
            p = ring[i]
            q = ring[(i + 1) % n]
            y0, y1 = p.y, q.y
            if y0 == y1:
                continue  # horizontal or zero-length edge
            if y0 < y1:
                if yc < y0 or yc >= y1:
                    continue
                direction = 1
            else:
                if yc < y1 or yc >= y0:
                    continue
                direction = -1
            x = p.x + (yc - y0) * (q.x - p.x) / (y1 - y0)
            result.append((x, direction))
    result.sort(key=lambda c: c[0])
    return result


def winding_at(rings: Sequence[Sequence[Point]], px, py, rule: str = NONZERO) -> bool:
    """Brute-force per-point classification (reference implementation).

    Casts the +x ray from (px, py) and applies the fill rule to the
    exact crossings.  px/py may be int, Fraction or (num, den).
    """
    px = to_frac(px)
    py = to_frac(py)
    winding = 0
    parity = 0
    for ring in rings:
        n = len(ring)
        for i in range(n):
            p = ring[i]
            q = ring[(i + 1) % n]
            y0, y1 = p.y, q.y
            if y0 == y1:
                continue
            if (y0 <= py) == (y1 <= py):
                continue  # py outside the half-open [lo, hi) interval
            x = p.x + (py - y0) * (q.x - p.x) / (y1 - y0)
            if x > px:
                parity ^= 1
                winding += 1 if y1 > y0 else -1
    if rule == NONZERO:
        return winding != 0
    return bool(parity)


def fill_row_spans(
    rings: Sequence[Sequence[Point]],
    yc: Fraction,
    x0: int,
    x1: int,
    rule: str = NONZERO,
    ox: Fraction = Fraction(0),
) -> List[Tuple[int, int]]:
    """Covered pixel spans [(x_start, x_end), ...] for one row.

    Covers pixel centres (ox + x + 1/2, yc) for x in [x0, x1);
    spans are half-open pixel ranges.
    """
    crossings = _crossings(rings, yc)
    total_wind = sum(d for _, d in crossings)
    total_count = len(crossings)
    spans: List[Tuple[int, int]] = []
    run_start = None
    idx = 0
    wind_le = 0   # winding of crossings with x <= current centre
    count_le = 0  # number of crossings with x <= current centre
    for x in range(x0, x1):
        xc = ox + x + _HALF
        while idx < total_count and crossings[idx][0] <= xc:
            wind_le += crossings[idx][1]
            count_le += 1
            idx += 1
        if rule == NONZERO:
            inside = (total_wind - wind_le) != 0
        else:
            inside = ((total_count - count_le) & 1) == 1
        if inside:
            if run_start is None:
                run_start = x
        elif run_start is not None:
            spans.append((run_start, x))
            run_start = None
    if run_start is not None:
        spans.append((run_start, x1))
    return spans
