"""Exact geometry primitives.

All coordinates are integers or ``fractions.Fraction`` (rationals).  No
floating point and no epsilon tolerances are used anywhere: every
predicate is an exact rational comparison.

Defined degenerate behaviour
----------------------------
* Zero-length edges (two identical consecutive vertices) are ignored.
* Rings with fewer than two distinct points cover nothing.
* Horizontal edges never generate scanline crossings; vertices are
  handled by a half-open scanline interval so each vertex is counted
  exactly once.
* A pixel center lying exactly on an edge or vertex is *covered*
  (closed-set semantics).  This is what makes two polygons sharing an
  edge compose without cracks and makes "fill(A) OR fill(B)" identical
  to "fill(union)".
* A singular transform (determinant exactly zero) collapses all
  geometry to zero area; layers using it are defined to cover nothing.
"""

from fractions import Fraction

EVENODD = "evenodd"
NONZERO = "nonzero"


def F(value):
    """Coerce an int/Fraction to Fraction."""
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


def norm_point(p):
    return (F(p[0]), F(p[1]))


def norm_rings(rings):
    return [[norm_point(p) for p in ring] for ring in rings]


class Transform:
    """Exact 2D affine transform: x' = a*x + b*y + tx, y' = c*x + d*y + ty."""

    def __init__(self, a=1, b=0, c=0, d=1, tx=0, ty=0):
        self.a, self.b, self.c, self.d = F(a), F(b), F(c), F(d)
        self.tx, self.ty = F(tx), F(ty)

    @property
    def determinant(self):
        return self.a * self.d - self.b * self.c

    @property
    def is_singular(self):
        # Exact zero test -- a singular transform collapses the plane and
        # is defined to produce empty coverage.
        return self.determinant == 0

    def apply(self, point):
        x, y = norm_point(point)
        return (self.a * x + self.b * y + self.tx,
                self.c * x + self.d * y + self.ty)

    def apply_rings(self, rings):
        return [[self.apply(p) for p in ring] for ring in rings]

    @staticmethod
    def translate(tx, ty):
        return Transform(tx=tx, ty=ty)

    @staticmethod
    def scale(sx, sy=None):
        return Transform(a=sx, d=sx if sy is None else sy)

    @staticmethod
    def shear(shx, shy=0):
        return Transform(b=shx, c=shy)


def rings_bbox(rings):
    """Exact bbox (minx, miny, maxx, maxy) of rings, or None if empty."""
    pts = [p for ring in rings for p in ring]
    if not pts:
        return None
    xs = [p[0] for p in pts]
    ys = [p[1] for p in pts]
    return (min(xs), min(ys), max(xs), max(ys))


def _on_segment(px, py, x1, y1, x2, y2):
    """Exact collinearity + bounding-box test (boundary predicate)."""
    if (px - x1) * (y2 - y1) != (py - y1) * (x2 - x1):
        return False
    return (min(x1, x2) <= px <= max(x1, x2)
            and min(y1, y2) <= py <= max(y1, y2))


def point_in_rings(px, py, rings, rule=NONZERO):
    """Reference point-in-polygon test with exact arithmetic.

    Boundary points are defined as covered.  ``rule`` is EVENODD or
    NONZERO.  Used as the per-pixel oracle in tests.
    """
    px, py = F(px), F(py)
    wind = 0
    parity = False
    for ring in rings:
        n = len(ring)
        for i in range(n):
            x1, y1 = norm_point(ring[i])
            x2, y2 = norm_point(ring[(i + 1) % n])
            if x1 == x2 and y1 == y2:
                continue  # degenerate zero-length edge
            if _on_segment(px, py, x1, y1, x2, y2):
                return True
            if y1 == y2:
                continue  # horizontal edges never cross the ray
            if min(y1, y2) <= py < max(y1, y2):
                xint = x1 + (py - y1) * (x2 - x1) / (y2 - y1)
                if xint > px:
                    if y2 > y1:
                        wind += 1
                    else:
                        wind -= 1
                    parity = not parity
    return wind != 0 if rule == NONZERO else parity
