"""Exact scanline polygon fill.

Pixels are sampled at their centers (x + 1/2, y + 1/2).  Scanline
intersections are computed and sorted as exact rationals, so coincident
edges, horizontal edges and vertices need no epsilon handling:

* horizontal edges produce no crossings (half-open y-interval);
* crossings at identical x are grouped and applied together;
* a pixel center exactly on a crossing / horizontal edge is boundary
  and is covered (closed-set semantics shared with the reference
  oracle in ``geometry.point_in_rings``).
"""

from fractions import Fraction
from math import ceil, floor

from .geometry import EVENODD, NONZERO, norm_rings

_HALF = Fraction(1, 2)


def _pixel_index_range(x_lo, x_hi, x0, width):
    """Exact index range of pixel centers strictly inside (x_lo, x_hi)."""
    i_lo = floor(x_lo - x0 - _HALF) + 1
    i_hi = ceil(x_hi - x0 - _HALF) - 1
    return max(i_lo, 0), min(i_hi, width - 1)


def fill_rows(rings, rule, x0, y0, width, height, counter=None):
    """Fill the pixel rect [x0, x0+width) x [y0, y0+height).

    ``rings`` is a list of rings (outer + holes); each ring is a list of
    (x, y) int/Fraction points, implicitly closed.  ``rule`` is EVENODD
    or NONZERO.  Returns a list of ``height`` rows of ``width`` bools.
    ``counter`` is an optional dict whose 'rows' entry is incremented
    per scanline actually computed (used to prove partial re-renders).
    """
    rings = norm_rings(rings)
    edges = []
    for ring in rings:
        n = len(ring)
        for i in range(n):
            p1, p2 = ring[i], ring[(i + 1) % n]
            if p1 == p2:
                continue  # degenerate zero-length edge: defined no-op
            edges.append((p1, p2))

    rows = []
    for j in range(y0, y0 + height):
        if counter is not None:
            counter["rows"] += 1
        ys = Fraction(2 * j + 1, 2)
        row = [False] * width

        # Vertices exactly on the scanline are boundary points -> covered.
        # This also covers local extrema, which produce no crossing under
        # the half-open rule.
        for ring in rings:
            for vx, vy in ring:
                if vy == ys:
                    off = vx - x0 - _HALF
                    if off.denominator == 1:
                        i = off.numerator
                        if 0 <= i < width:
                            row[i] = True

        crossings = []  # (exact x, winding delta)
        for (x1, y1), (x2, y2) in edges:
            if y1 == y2:
                # Horizontal edge lying exactly on the sample line:
                # pixel centers on it are boundary -> covered.
                if y1 == ys:
                    lo, hi = min(x1, x2), max(x1, x2)
                    i_lo = ceil(lo - x0 - _HALF)
                    i_hi = floor(hi - x0 - _HALF)
                    for i in range(max(i_lo, 0), min(i_hi, width - 1) + 1):
                        row[i] = True
                continue
            if min(y1, y2) <= ys < max(y1, y2):
                x = x1 + (ys - y1) * (x2 - x1) / (y2 - y1)
                crossings.append((x, 1 if y2 > y1 else -1))
        crossings.sort(key=lambda c: c[0])

        wind = 0
        parity = False
        prev = None
        k = 0
        while k < len(crossings):
            gx = crossings[k][0]
            inside = wind != 0 if rule == NONZERO else parity
            if prev is not None and inside:
                i_lo, i_hi = _pixel_index_range(prev, gx, x0, width)
                for i in range(i_lo, i_hi + 1):
                    row[i] = True
            # Pixel center exactly on this crossing is boundary -> covered.
            off = gx - x0 - _HALF
            if off.denominator == 1:
                i = off.numerator
                if 0 <= i < width:
                    row[i] = True
            # Apply all coincident crossings as one group.
            while k < len(crossings) and crossings[k][0] == gx:
                wind += crossings[k][1]
                parity = not parity
                k += 1
            prev = gx
        rows.append(row)
    return rows
