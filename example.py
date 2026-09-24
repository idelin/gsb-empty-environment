from fractions import Fraction

from bezier_flatten import FlattenError, flatten_bezier, flatten_bezier_intervals


def show(title, points, tolerance, *, max_depth=64):
    print(title)
    try:
        segments = flatten_bezier(
            points,
            tolerance,
            max_depth=max_depth,
        )
    except FlattenError as error:
        print(f"  FlattenError: {error}")
        print()
        return

    print(f"  pieces: {len(segments)}")
    print(f"  intervals: {flatten_bezier_intervals(points, tolerance, max_depth=max_depth)}")
    for segment in segments[:4]:
        print(
            "  "
            f"[{segment.t_start}, {segment.t_end}] "
            f"{segment.start} -> {segment.end}, "
            f"squared bound={segment.squared_bound}"
        )
    if len(segments) > 4:
        print(f"  ... {len(segments) - 4} more pieces")
    print()


def main():
    show(
        "straight quadratic control points",
        [(0, 0), (2, 2), (4, 4)],
        Fraction(1, 10),
    )
    show(
        "quadratic arc with rational tolerance",
        [(0, 0), (0, 8), (8, 8)],
        Fraction(1, 10),
    )
    show(
        "cubic cusp at t=1/2",
        [(0, 0), (1, 0), (0, 1), (1, -1)],
        Fraction(1, 100),
    )
    show(
        "depth limit exhausted",
        [(0, 0), (0, 8), (8, 8)],
        Fraction(1, 1_000_000),
        max_depth=1,
    )


if __name__ == "__main__":
    main()
