"""Independent, exact validator for a triangulation of a polygon with holes.

Checks, using only exact rational arithmetic:

1. every triangle is non-degenerate and consistently oriented (CCW);
2. area conservation: sum of triangle areas equals
   area(outer) - sum(area(hole)) exactly;
3. containment: every triangle interior lies inside the outer ring and
   outside every hole (centroid test + no proper edge crossings + no ring
   vertex strictly inside a triangle);
4. no two triangles overlap in their interiors.
"""
from geom import (INSIDE, OUTSIDE, as_point, orient, properly_intersect,
                  point_in_ring, signed_area2, point_strictly_in_triangle)


class ValidationError(ValueError):
    """The triangulation failed validation."""


def _edges(ring):
    n = len(ring)
    return [(ring[i], ring[(i + 1) % n]) for i in range(n)]


def _check_containment(tri, outer_pts, hole_pts_list):
    cx = (tri[0][0] + tri[1][0] + tri[2][0]) / 3
    cy = (tri[0][1] + tri[1][1] + tri[2][1]) / 3
    centroid = (cx, cy)
    if point_in_ring(centroid, outer_pts) != INSIDE:
        raise ValidationError(f"triangle {tri} escapes the outer ring")
    for k, hp in enumerate(hole_pts_list, 1):
        if point_in_ring(centroid, hp) != OUTSIDE:
            raise ValidationError(f"triangle {tri} reaches into hole {k}")
    tri_edges = _edges(tri)
    for ring in [outer_pts] + hole_pts_list:
        for e in _edges(ring):
            for te in tri_edges:
                if properly_intersect(e[0], e[1], te[0], te[1]):
                    raise ValidationError(
                        f"triangle {tri} crosses the boundary at {e}")
        for w in ring:
            if point_strictly_in_triangle(w, *tri):
                raise ValidationError(
                    f"boundary vertex {w} lies inside triangle {tri}")


def _triangles_overlap(t1, t2):
    if set(t1) == set(t2):
        return True
    for e1 in _edges(t1):
        for e2 in _edges(t2):
            if properly_intersect(e1[0], e1[1], e2[0], e2[1]):
                return True
    for p in t1:
        if point_strictly_in_triangle(p, *t2):
            return True
    for p in t2:
        if point_strictly_in_triangle(p, *t1):
            return True
    return False


def validate_triangulation(outer, holes, triangles):
    """Raise ``ValidationError`` unless ``triangles`` exactly tiles the region.

    Returns True on success.
    """
    outer_pts = [as_point(p) for p in outer]
    hole_pts_list = [[as_point(p) for p in h] for h in (holes or [])]
    total = abs(signed_area2(outer_pts))
    for hp in hole_pts_list:
        total -= abs(signed_area2(hp))
    if total <= 0:
        raise ValidationError("region has no positive area")
    if not triangles:
        raise ValidationError("empty triangulation")

    acc = 0
    exact_tris = []
    for t in triangles:
        tri = tuple(as_point(p) for p in t.points)
        if len(tri) != 3:
            raise ValidationError(f"not a triangle: {tri}")
        o = orient(*tri)
        if o <= 0:
            raise ValidationError(
                f"degenerate or clockwise triangle {tri} (area2={o})")
        acc += o
        exact_tris.append(tri)
        _check_containment(tri, outer_pts, hole_pts_list)

    if acc != total:
        raise ValidationError(
            f"area mismatch: triangles sum to {acc}/2 = {acc / 2}, "
            f"region is {total}/2 = {total / 2}")

    for i in range(len(exact_tris)):
        for j in range(i + 1, len(exact_tris)):
            if _triangles_overlap(exact_tris[i], exact_tris[j]):
                raise ValidationError(
                    f"triangles {i} and {j} overlap: "
                    f"{exact_tris[i]} vs {exact_tris[j]}")
    return True
