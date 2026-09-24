"""Runnable demo: python3.11 example.py"""
from fractions import Fraction

from bezier_flatten import flatten_cubic, flatten_quadratic


def show(title, result):
    print(f"{title}")
    print(f"  converged={result.converged}  segments={len(result.segments)}")
    print(f"  parameter intervals: "
          f"{[(str(a), str(b)) for a, b in result.intervals]}")
    print(f"  polyline points: "
          f"{[(str(x), str(y)) for x, y in result.points]}")
    if result.unmet_intervals:
        print(f"  UNMET intervals (tolerance not certified): "
              f"{[(str(a), str(b)) for a, b in result.unmet_intervals]}")
    print()


# 1. Quadratic with integer coordinates, tolerance 1/8.
show(
    "quadratic (0,0)-(0,1)-(1,1), tol=1/8",
    flatten_quadratic((0, 0), (0, 1), (1, 1), Fraction(1, 8)),
)

# 2. Collinear control points: certified straight line -> single segment.
show(
    "cubic collinear (0,0)-(1,2)-(3,6)-(4,8), tol=1/1000",
    flatten_cubic((0, 0), (1, 2), (3, 6), (4, 8), Fraction(1, 1000)),
)

# 3. Cubic with a genuine cusp at t=1/3: subdivision concentrates there.
show(
    "cusp cubic (0,0)-(2,1)-(1,1)-(-3,-3), tol=1/500",
    flatten_cubic((0, 0), (2, 1), (1, 1), (-3, -3), Fraction(1, 500)),
)

# 4. Depth cap too small for the tolerance: failure is reported explicitly.
show(
    "cubic (0,0)-(0,4)-(4,0)-(4,4), tol=1/1000, max_depth=2",
    flatten_cubic((0, 0), (0, 4), (4, 0), (4, 4), Fraction(1, 1000),
                  max_depth=2),
)
