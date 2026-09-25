"""Sample runs for polygon_clipper (Python 3.11+, standard library only)."""

from fractions import Fraction

from polygon_clipper import (
    NonConvexWindowError,
    SelfIntersectionError,
    clip_polygon,
    polygon_area,
)


def fmt_pt(p):
    return f"({p[0]}, {p[1]})"


def show(title, subject, window):
    print(f"== {title} ==")
    print(f"subject area: {polygon_area([(Fraction(x), Fraction(y)) for x, y in subject])}")
    result = clip_polygon(subject, window)
    if not result.regions:
        print("result: empty (no intersection)\n")
        return result
    print(f"result: {len(result.polygons)} polygon region(s), "
          f"{len(result.degenerate)} degenerate region(s), "
          f"total area {result.total_area}")
    for k, region in enumerate(result.regions):
        tag = " [degenerate]" if region.degenerate else ""
        print(f"  region {k}: kind={region.kind}{tag} area={region.area}")
        print(f"    vertices: {', '.join(fmt_pt(p) for p in region.vertices)}")
        for e in region.edges:
            print(f"    edge {fmt_pt(e.a)} -> {fmt_pt(e.b)}"
                  f"  from subject edges {e.subject_edges or '-'}"
                  f"  window edges {e.window_edges or '-'}")
        if region.kind == "point":
            print(f"    point lies on subject edges {region.on_subject_edges},"
                  f" window edges {region.on_window_edges}")
    print()
    return result


def main():
    # 1. Concave subject clipped into two connected components.
    u_shape = [(0, 0), (4, 0), (4, 4), (3, 4), (3, 1), (1, 1), (1, 4), (0, 4)]
    u_window = [(-1, 2), (5, 2), (5, 5), (-1, 5)]
    r1 = show("U-shaped polygon clipped by a half-strip (2 regions)",
              u_shape, u_window)

    # 2. Exact rational intersections (no floating point involved).
    r2 = show("Triangle clipped at x = 1/2 (exact rationals)",
              [(0, 0), (2, 0), (0, 2)],
              [(Fraction(1, 2), -1), (3, -1), (3, 3), (Fraction(1, 2), 3)])

    # 3. Degenerate results: intersection collapses to a line / a point.
    show("Degenerate line: only one subject edge lies in the window",
         [(0, 0), (2, 0), (0, -2)], [(0, 0), (4, 0), (4, 4), (0, 4)])
    show("Degenerate point: triangle touches the window corner",
         [(-2, -1), (-1, -2), (0, 0)], [(0, 0), (4, 0), (4, 4), (0, 4)])

    # 4. Rejected inputs.
    print("== Rejected inputs ==")
    try:
        clip_polygon([(0, 0), (2, 2), (2, 0), (0, 2)], [(0, 0), (4, 0), (4, 4), (0, 4)])
    except SelfIntersectionError as exc:
        print(f"bowtie subject rejected: SelfIntersectionError: {exc}")
    try:
        clip_polygon([(0, 0), (1, 0), (0, 1)], [(0, 0), (4, 0), (4, 4), (2, 1), (0, 4)])
    except NonConvexWindowError as exc:
        print(f"non-convex window rejected: NonConvexWindowError: {exc}")
    print()

    # 5. Invariants: area bound and idempotency.
    print("== Invariants ==")
    for name, subject, window in [
        ("u_shape", u_shape, u_window),
        ("tri_cut", [(0, 0), (2, 0), (0, 2)],
         [(Fraction(1, 2), -1), (3, -1), (3, 3), (Fraction(1, 2), 3)]),
    ]:
        first = clip_polygon(subject, window)
        subject_area = polygon_area([(Fraction(x), Fraction(y)) for x, y in subject])
        ok_area = first.total_area <= subject_area
        again = [clip_polygon(list(r.vertices), window) for r in first.polygons]
        ok_idem = (all(len(r.polygons) == 1 and not r.degenerate for r in again)
                   and sum((r.total_area for r in again), Fraction(0)) == first.total_area)
        print(f"{name}: total area {first.total_area} <= subject area "
              f"{subject_area}: {ok_area}; idempotent re-clip: {ok_idem}")


if __name__ == "__main__":
    main()
