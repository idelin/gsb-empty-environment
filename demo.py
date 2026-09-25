"""Runnable samples for the topology-preserving polyline simplifier.

Run with:  python3 demo.py
"""

from fractions import Fraction

from polysimplify import simplify_polylines

F = Fraction


def show(title, polylines, result):
    print("=" * 68)
    print(title)
    print("-" * 68)
    for i, (before, after) in enumerate(zip(polylines, result.polylines)):
        print(f"  polyline {i}: {len(before)} -> {len(after)} points")
        print(f"    after: {after}")
    if result.removed:
        print("  removed points (exact squared distance to final chain):")
        for rp in result.removed:
            print(f"    {rp.point}  dist^2 = {rp.dist2_to_result}"
                  f"  (~{float(rp.dist2_to_result) ** 0.5:.4f})")
    print(f"  certified max error^2 = {result.max_error2}"
          f"  (~{float(result.max_error2) ** 0.5:.4f})")
    print(f"  threshold^2           = {result.threshold2}"
          f"  (respected: {result.max_error2 <= result.threshold2})")
    print()


def main():
    # 1. Two regions sharing a seam: the common boundary is simplified once
    #    and stays byte-identical on both sides.
    left = [(0, 0), (10, 0), (10, 4), (11, 5), (10, 6), (10, 10), (0, 10)]
    right = [(10, 0), (20, 0), (20, 10), (10, 10), (10, 6), (11, 5), (10, 4)]
    r = simplify_polylines([left, right], threshold=F(3, 2),
                           closed=[True, True])
    show("1. Two regions, shared seam, threshold 3/2", [left, right], r)

    # 2. Narrow channel: cutting the corner of wall A is within threshold
    #    but the chord would cross wall B, so the corner is kept.
    wall_a = [(0, 0), (5, 0), (5, 5)]
    wall_b = [(4, 6), (F(9, 2), F(1, 2))]
    r = simplify_polylines([wall_a, wall_b], threshold=10)
    show("2. Narrow channel, threshold 10 (corner must survive)",
         [wall_a, wall_b], r)
    r = simplify_polylines([wall_a], threshold=10)
    show("2b. Same wall without the opposite wall (cut is legal)",
         [wall_a], r)

    # 3. Threshold 0: only strictly redundant points (duplicates and
    #    exactly collinear interior points) are removed.
    p = [(0, 0), (1, 0), (2, 0), (2, 1), (3, 1), (3, 1), (4, 0)]
    r = simplify_polylines([p], threshold=0)
    show("3. Threshold 0 removes only strictly redundant points", [p], r)

    # 4. Rational coordinates: exact arithmetic, exact certified bound.
    p = [(0, 0), (F(1, 3), F(1, 2)), (F(2, 3), F(-1, 2)), (1, 0)]
    r = simplify_polylines([p], threshold=F(3, 4))
    show("4. Rational coordinates, threshold 3/4", [p], r)


if __name__ == "__main__":
    main()
