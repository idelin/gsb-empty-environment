"""Runnable examples for rect_union.  Run:  python3.11 demo.py"""

from fractions import Fraction

from rect_union import Rectangle, union


def show(title, rects):
    result = union(rects)
    print(f"== {title} ==")
    print(f"  rectangles : {rects}")
    print(f"  area       : {result.area}")
    print(f"  perimeter  : {result.perimeter}")
    for k, polygon in enumerate(result.polygons):
        print(f"  polygon {k}  : outer (CCW) {list(polygon.outer)}")
        for hole in polygon.holes:
            print(f"             hole (CW)  {list(hole)}")
    if not result.polygons:
        print("  polygons   : (none)")
    print()


if __name__ == "__main__":
    show(
        "two adjacent unit squares (edge contact)",
        [Rectangle(0, 0, 1, 1), Rectangle(1, 0, 2, 1)],
    )
    show(
        "two unit squares touching at a corner (no false merge)",
        [Rectangle(0, 0, 1, 1), Rectangle(1, 1, 2, 2)],
    )
    show(
        "zero-area rectangles contribute nothing",
        [Rectangle(0, 0, 1, 0), Rectangle(2, 2, 2, 5), Rectangle(0, 0, 2, 1)],
    )
    show(
        "containment and partial overlap",
        [Rectangle(0, 0, 4, 4), Rectangle(1, 1, 2, 2), Rectangle(3, 3, 6, 5)],
    )
    show(
        "a square frame with a hole",
        [
            Rectangle(0, 0, 3, 1),
            Rectangle(0, 2, 3, 3),
            Rectangle(0, 1, 1, 2),
            Rectangle(2, 1, 3, 2),
        ],
    )
    show(
        "exact rationals: overlapping 1x1 squares shifted by 1/2",
        [
            Rectangle(0, 0, 1, 1),
            Rectangle(Fraction(1, 2), Fraction(1, 2),
                      Fraction(3, 2), Fraction(3, 2)),
        ],
    )
