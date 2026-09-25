"""Runnable demo: triangulate polygons with holes and validate the result.

Run from the repository root with Python 3.11:

    python3.11 examples/demo.py
"""

import os
import sys
from fractions import Fraction as F

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from polytri import triangulate, validate_triangulation  # noqa: E402


def fmt_point(p):
    def fmt(v):
        return str(v.numerator) if v.denominator == 1 else str(v)
    return f"({fmt(p[0])}, {fmt(p[1])})"


def show(title, outer, holes):
    print("=" * 74)
    print(title)
    print("=" * 74)
    print(f"outer ring : {[fmt_point(p) for p in outer]}")
    for k, h in enumerate(holes):
        print(f"hole[{k}]    : {[fmt_point(p) for p in h]}")

    triangles = triangulate(outer, holes)
    print(f"\n{len(triangles)} triangles:")
    for i, t in enumerate(triangles):
        pts = ", ".join(fmt_point(p) for p in t.vertices)
        print(f"  T{i:02d}  area={str(t.area):>6}  src={t.source_label:<18}  [{pts}]")

    report = validate_triangulation(outer, holes, triangles)
    print("\nvalidation:")
    print(report.summary())
    print()
    return triangles, report


def main():
    # 1. square with a square hole (integer coordinates)
    show(
        "Example 1: 10x10 square with an axis-aligned square hole",
        outer=[(0, 0), (10, 0), (10, 10), (0, 10)],
        holes=[[(3, 3), (7, 3), (7, 7), (3, 7)]],
    )

    # 2. rational coordinates + redundant collinear vertices on both rings
    show(
        "Example 2: rational coordinates and collinear vertices",
        outer=[(0, 0), (F(9, 2), 0), (9, 0), (9, 6), (0, 6)],
        holes=[[(2, 2), (F(7, 2), F(5, 2)), (5, 2), (5, 4), (2, 4)]],
    )

    # 3. the ray from the hole's rightmost vertex hits an outer vertex exactly
    show(
        "Example 3: bridge lands exactly on a vertex (degenerate ray hit)",
        outer=[(0, 0), (12, 0), (14, 6), (12, 12), (0, 12)],
        holes=[[(4, 4), (6, 6), (4, 8)]],
    )

    # 4. concave outer ring with two holes
    show(
        "Example 4: concave outer ring with two holes",
        outer=[(0, 0), (12, 0), (12, 4), (6, 4), (6, 8), (12, 8), (12, 12), (0, 12)],
        holes=[
            [(8, 1), (11, 1), (11, 3), (8, 3)],
            [(1, 5), (4, 5), (4, 10), (1, 10)],
        ],
    )

    # 5. the validator catches a tampered triangulation
    print("=" * 74)
    print("Example 5: the validator rejects a tampered triangulation")
    print("=" * 74)
    outer = [(0, 0), (10, 0), (10, 10), (0, 10)]
    holes = [[(3, 3), (7, 3), (7, 7), (3, 7)]]
    triangles = triangulate(outer, holes)
    tampered = triangles[:-1]  # drop one triangle: area no longer conserved
    report = validate_triangulation(outer, holes, tampered)
    print(report.summary())


if __name__ == "__main__":
    main()
