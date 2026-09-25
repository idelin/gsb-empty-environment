"""Per-segment collision/clearance validation of a planned path.

This is an independent re-check (it does not trust the planner): each
segment of the (possibly simplified) path is tested exactly against
every obstacle and against the eroded warehouse bounds.
"""

from dataclasses import dataclass, field
from fractions import Fraction

from geom import (
    point_in_polygon,
    point_polygon_dist2,
    seg_polygon_dist2,
    segment_enters_interior,
    to_frac,
    to_point,
)
from planner import Bounds


@dataclass
class SegmentReport:
    index: int
    start: tuple
    end: tuple
    clearance2: list          # exact squared clearance per obstacle
    min_clearance2: object    # Fraction, or None when no obstacles
    required2: Fraction       # radius**2
    within_bounds: bool
    enters_interior: bool
    ok: bool


def validate_path(path, obstacles, radius, bounds):
    """Validate every segment of ``path`` exactly; returns reports."""
    r = to_frac(radius)
    r2 = r * r
    b = bounds if isinstance(bounds, Bounds) else Bounds.of(*bounds)
    polys = [[to_point(v) for v in poly] for poly in obstacles]
    pts = [to_point(p) for p in path]
    if not pts:
        raise ValueError("empty path")
    if len(pts) == 1:
        pts = pts * 2  # degenerate single-point path: validate as a point
    reports = []
    pairs = zip(pts, pts[1:]) if len(pts) > 2 or pts[0] != pts[1] else [(pts[0], pts[0])]
    for idx, (p, q) in enumerate(pairs):
        clearances = [seg_polygon_dist2(p, q, poly) for poly in polys]
        enters = any(
            point_in_polygon(p, poly)
            or point_in_polygon(q, poly)
            or segment_enters_interior(p, q, poly)
            for poly in polys
        )
        within = b.eroded_contains(p, r) and b.eroded_contains(q, r)
        min_c = min(clearances) if clearances else None
        ok = (
            within
            and not enters
            and all(c >= r2 for c in clearances)
        )
        reports.append(
            SegmentReport(
                index=idx,
                start=p,
                end=q,
                clearance2=clearances,
                min_clearance2=min_c,
                required2=r2,
                within_bounds=within,
                enters_interior=enters,
                ok=ok,
            )
        )
    return reports


def path_valid(path, obstacles, radius, bounds):
    return all(rep.ok for rep in validate_path(path, obstacles, radius, bounds))
