"""Independent validator for triangulations of polygons with holes.

The validator re-derives everything from the *input rings* and the *output
triangles* only, using exact rational arithmetic.  It checks:

* ``non_degenerate``      - every triangle is CCW with strictly positive
                            area (no zero-area output, e.g. from duplicated
                            bridge vertices);
* ``vertices_from_input`` - every triangle corner is a vertex of the
                            (cleaned) input rings, i.e. no Steiner points;
* ``inside_region``       - every triangle's centroid lies strictly inside
                            the outer ring and strictly outside every hole,
                            no ring edge properly crosses a triangle edge,
                            and no ring vertex lies strictly inside a
                            triangle (so no triangle interior enters a hole
                            or leaves the outer ring);
* ``triangles_disjoint``  - triangle interiors are pairwise disjoint;
* ``area_conservation``   - sum of triangle areas equals exactly
                            area(outer) - sum(area(hole)).
"""

from dataclasses import dataclass, field
from fractions import Fraction

from .exact import (
    INSIDE,
    OUTSIDE,
    area2,
    classify_point,
    in_triangle_strict,
    orient,
    proper_cross,
    to_point,
)
from .preprocess import PolygonError, normalize_polygon, ring_edges


@dataclass
class ValidationReport:
    """Result of :func:`validate_triangulation`."""

    ok: bool
    checks: dict = field(default_factory=dict)
    errors: list = field(default_factory=list)
    triangle_area: Fraction = Fraction(0)
    region_area: Fraction = Fraction(0)

    def summary(self):
        lines = []
        for name, passed in self.checks.items():
            lines.append(f"  [{'PASS' if passed else 'FAIL'}] {name}")
        lines.append(f"  triangle area sum : {self.triangle_area}")
        lines.append(f"  region area       : {self.region_area}")
        lines.append(f"  overall           : {'OK' if self.ok else 'INVALID'}")
        for err in self.errors:
            lines.append(f"  error: {err}")
        return "\n".join(lines)


def _triangle_points(t):
    vs = getattr(t, "vertices", t)
    pts = tuple(to_point(p) for p in vs)
    if len(pts) != 3:
        raise PolygonError(f"triangle must have 3 vertices, got {len(pts)}")
    return pts


def _tri_edges(t):
    return [(t[0], t[1]), (t[1], t[2]), (t[2], t[0])]


def validate_triangulation(outer, holes, triangles):
    """Validate ``triangles`` as a triangulation of the region outer \\ holes.

    ``triangles`` may contain :class:`polytri.Triangle` objects or plain
    3-tuples of points.  Returns a :class:`ValidationReport`.
    """
    errors = []
    checks = {}
    try:
        outer_ring, hole_rings = normalize_polygon(outer, holes)
    except PolygonError as exc:
        return ValidationReport(ok=False, checks={"input_valid": False},
                                errors=[str(exc)])
    checks["input_valid"] = True

    tris = []
    for t in triangles:
        try:
            tris.append(_triangle_points(t))
        except PolygonError as exc:
            errors.append(str(exc))
    if errors:
        checks["non_degenerate"] = False
        return ValidationReport(False, checks, errors)

    # 1. strictly positive area, CCW
    nondeg = all(orient(*t) > 0 for t in tris)
    if not nondeg:
        errors.append("found degenerate or clockwise triangle")
    checks["non_degenerate"] = nondeg

    # 2. corners must come from the input rings
    allowed = set(outer_ring)
    for h in hole_rings:
        allowed.update(h)
    from_input = all(p in allowed for t in tris for p in t)
    if not from_input:
        errors.append("triangle corner is not an input ring vertex")
    checks["vertices_from_input"] = from_input

    # 3. every triangle stays inside the region (outer minus holes)
    all_rings = [outer_ring] + hole_rings
    all_edges = [e for ring in all_rings for e in ring_edges(ring)]
    inside_ok = True
    for t in tris:
        cen = (
            (t[0][0] + t[1][0] + t[2][0]) / 3,
            (t[0][1] + t[1][1] + t[2][1]) / 3,
        )
        if classify_point(outer_ring, cen) != INSIDE:
            inside_ok = False
            errors.append(f"triangle {t} centroid outside outer ring")
        for k, h in enumerate(hole_rings):
            if classify_point(h, cen) != OUTSIDE:
                inside_ok = False
                errors.append(f"triangle {t} centroid inside hole {k}")
        for te in _tri_edges(t):
            for e0, e1 in all_edges:
                if proper_cross(te[0], te[1], e0, e1):
                    inside_ok = False
                    errors.append(f"triangle {t} edge crosses the boundary")
        for ring in all_rings:
            for v in ring:
                if in_triangle_strict(v, *t):
                    inside_ok = False
                    errors.append(f"boundary vertex {v} strictly inside triangle {t}")
    checks["inside_region"] = inside_ok

    # 4. pairwise disjoint interiors
    disjoint = True
    for i in range(len(tris)):
        for j in range(i + 1, len(tris)):
            t1, t2 = tris[i], tris[j]
            if set(t1) == set(t2):
                disjoint = False
                errors.append(f"triangles {i} and {j} are identical")
                continue
            overlap = False
            for e1 in _tri_edges(t1):
                for e2 in _tri_edges(t2):
                    if proper_cross(e1[0], e1[1], e2[0], e2[1]):
                        overlap = True
            if not overlap:
                overlap = any(in_triangle_strict(v, *t2) for v in t1) or any(
                    in_triangle_strict(v, *t1) for v in t2
                )
            if overlap:
                disjoint = False
                errors.append(f"triangles {i} and {j} overlap")
    checks["triangles_disjoint"] = disjoint

    # 5. exact area conservation
    total = sum((orient(*t) for t in tris), Fraction(0)) / 2
    expected = (area2(outer_ring) + sum(area2(h) for h in hole_rings)) / 2
    conservation = total == expected
    if not conservation:
        errors.append(
            f"area mismatch: triangles sum to {total}, region area is {expected}"
        )
    checks["area_conservation"] = conservation

    ok = all(checks.values()) and not errors
    return ValidationReport(ok, checks, errors, total, expected)
