"""Demo: exact triangulation of a polygon with holes (Python 3.11, stdlib)."""
from fractions import Fraction

from geom import signed_area2
from triangulate import triangulate
from validate import validate_triangulation


def fmt(p):
    return f"({p[0]}, {p[1]})"


def main():
    # Outer ring with two collinear vertices (5,0) and (10,6) on its edges.
    outer = [(0, 0), (5, 0), (10, 0), (10, 6), (10, 10), (0, 10)]
    # An integer square hole and a rational-coordinate rectangular hole.
    hole1 = [(2, 2), (6, 2), (6, 6), (2, 6)]
    hole2 = [(Fraction(15, 2), 8), (9, 8), (9, 9), (Fraction(15, 2), 9)]
    holes = [hole1, hole2]

    triangles = triangulate(outer, holes)

    print(f"outer ring   : {' '.join(fmt(p) for p in outer)}")
    for k, h in enumerate(holes, 1):
        print(f"hole {k}       : {' '.join(fmt(p) for p in h)}")
    print(f"\n{len(triangles)} triangles:")
    total = Fraction(0)
    for i, t in enumerate(triangles):
        total += t.area
        print(f"  T{i:2d} area={str(t.area):6s} "
              f"src={t.source}  "
              f"{' '.join(fmt(p) for p in t.points)}")

    region = (signed_area2(outer) - sum(signed_area2(h) for h in holes)) / 2
    print(f"\nsum of triangle areas : {total}")
    print(f"outer - holes         : {region}")
    validate_triangulation(outer, holes, triangles)
    print("validator             : OK (exact area conservation, "
          "no triangle in any hole, no overlaps)")


if __name__ == "__main__":
    main()
