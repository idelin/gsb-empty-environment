"""Runnable demo: min-area oriented bounding rectangle with exact arithmetic."""

from fractions import Fraction

from min_area_rect import (
    PointSet,
    brute_force_min_area,
    min_area_rectangle,
    verify_certificate,
)


def show(title, points):
    print("=" * 72)
    print(title)
    cert = min_area_rectangle(points)
    print(f"  points            : {points}")
    print(f"  exact area        : {cert.area}  (float ~ {float(cert.area):.6f})")
    print(f"  supporting edge   : {cert.supporting_edge}")
    print(f"  direction u       : {cert.direction}")
    print(f"  proj range p.u    : [{cert.u_range[0]}, {cert.u_range[1]}]")
    print(f"  proj range p.v    : [{cert.v_range[0]}, {cert.v_range[1]}]")
    print("  four extremes     : "
          f"u_min={cert.extreme_points['u_min']}, u_max={cert.extreme_points['u_max']}, "
          f"v_min={cert.extreme_points['v_min']}, v_max={cert.extreme_points['v_max']}")
    print("  corners (CCW)     :")
    for c in cert.corners:
        print(f"      ({c[0]}, {c[1]})")
    print(f"  verify_certificate: {verify_certificate(cert, points)}")
    print(f"  matches brute force edge enumeration: "
          f"{cert == brute_force_min_area(points)}")


if __name__ == "__main__":
    show("Case 1: tilted rectangle (exact optimum beats axis-aligned frame)",
         [(0, 0), (6, 2), (4, 8), (-2, 6)])

    show("Case 2: near-collinear points at 1e9 scale (no float, no negative area)",
         [(0, 0), (10**9, 0), (10**9, 1), (0, 1), (5 * 10**8, 0)])

    show("Case 3: rational coordinates",
         [(Fraction(1, 3), Fraction(1, 2)),
          (Fraction(5, 6), Fraction(-1, 4)),
          (Fraction(-2, 3), Fraction(7, 8)), (0, 0)])

    show("Case 4: single point (degenerate, area 0)", [(5, -7)])
    show("Case 5: collinear points (degenerate, area 0)",
         [(k, 2 * k) for k in range(-2, 3)])

    print("=" * 72)
    print("Case 6: batch withdrawal, duplicate identity, removing unique extreme")
    ps = PointSet([(0, 0), (6, 0), (6, 4), (0, 4), (3, 2)])
    dup = ps.add((6, 4))
    ids = list(ps._pts)
    print(f"  inserted ids      : {ids}; duplicate id {dup} at (6,4)")
    print(f"  initial area      : {ps.minimum_area_rectangle().area}")
    ps.remove_many([dup, ids[2]])
    print(f"  after removing duplicate + unique top-right extreme (6,4):")
    print(f"  remaining points  : {ps.points()}")
    cert = ps.minimum_area_rectangle()
    print(f"  new exact area    : {cert.area}")
    print(f"  verify_certificate: {verify_certificate(cert, ps.points())}")

    print("=" * 72)
    print("Case 7: empty input")
    try:
        PointSet().minimum_area_rectangle()
    except ValueError as e:
        print(f"  ValueError: {e}")
