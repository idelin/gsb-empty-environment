from fractions import Fraction

from rectilinear_geometry import analyze_rectangles


def show(title, rectangles):
    result = analyze_rectangles(rectangles)
    print(title)
    print("  area =", result.area)
    print("  perimeter =", result.perimeter)
    print("  outer_perimeter =", result.outer_perimeter)
    print("  outer_rings =", result.outer_rings)
    print("  hole_rings =", result.hole_rings)
    print()


show("two side-adjacent unit squares", [(0, 0, 1, 1), (1, 0, 2, 1)])
show("two corner-touching unit squares", [(0, 0, 1, 1), (1, 1, 2, 2)])
show(
    "a square frame made from four filled bars",
    [
        (0, 0, 3, 1),
        (0, 2, 3, 3),
        (0, 1, 1, 2),
        (2, 1, 3, 2),
    ],
)
show(
    "rational coordinates",
    [
        (Fraction(0), Fraction(0), Fraction(1, 2), Fraction(2, 3)),
        (Fraction(1, 2), Fraction(0), 1, Fraction(2, 3)),
        ("0", "0", "1/2", "0"),
    ],
)
