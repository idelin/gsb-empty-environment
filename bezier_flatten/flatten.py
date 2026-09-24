"""Flatten Bézier curves using de Casteljau subdivision and an exact bound.

All coordinates and the requested tolerance are represented with
``fractions.Fraction``.  The implementation therefore does not use a floating
point epsilon for incidence, ordering, or continuation decisions.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Sequence


Point = tuple[Fraction, Fraction]
RationalLike = int | Fraction


class FlattenError(ValueError):
    """Raised when an argument is invalid or the depth limit is exhausted."""


@dataclass(frozen=True)
class FlattenSegment:
    """One accepted polyline piece, expressed in the original parameter.

    ``start`` and ``end`` are exact points on the original curve at
    ``t_start`` and ``t_end`` respectively.
    """

    t_start: Fraction
    t_end: Fraction
    start: Point
    end: Point
    squared_bound: Fraction


def flatten_bezier_intervals(
    control_points: Sequence[Sequence[RationalLike]],
    tolerance: RationalLike,
    *,
    max_depth: int = 64,
) -> list[tuple[Fraction, Fraction]]:
    """Return the accepted parameter intervals of a flattening."""

    return [
        (segment.t_start, segment.t_end)
        for segment in flatten_bezier(
            control_points,
            tolerance,
            max_depth=max_depth,
        )
    ]


def flatten_bezier(
    control_points: Sequence[Sequence[RationalLike]],
    tolerance: RationalLike,
    *,
    max_depth: int = 64,
) -> list[FlattenSegment]:
    """Split a quadratic or cubic Bézier curve into straight polyline pieces.

    For every returned interval, every curve point in that interval is at
    Euclidean distance at most ``tolerance`` from the returned closed chord
    segment.  This guarantee follows from the geometric control-point bound:

    A Bézier curve lies in the convex hull of its control points.  Squared
    distance to a fixed closed segment is a convex function.  Hence squared
    distance from a curve point to the endpoint chord is no greater than the
    maximum squared distance of the interval's control points to that chord.
    The maximum of those exact squared distances is the segment's
    ``squared_bound``.

    A control point that is collinear but lies outside the chord is measured
    to the nearest endpoint.  Such an overshooting "straight" control polygon
    therefore cannot be accepted as one piece until subdivision proves that
    the relevant monotonic part is bounded.
    """

    points = _as_points(control_points)
    if len(points) not in (3, 4):
        raise FlattenError("exactly three quadratic or four cubic points are required")

    eps = _as_fraction(tolerance, "tolerance")
    if eps <= 0:
        raise FlattenError("tolerance must be positive")
    if isinstance(max_depth, bool) or not isinstance(max_depth, int):
        raise FlattenError("max_depth must be an integer")
    if max_depth < 0:
        raise FlattenError("max_depth must be non-negative")

    tolerance_squared = eps * eps
    intervals: list[FlattenSegment] = []

    # Iterative depth-first subdivision avoids relying on Python recursion depth.
    stack: list[tuple[Fraction, Fraction, tuple[Point, ...], int]] = [
        (Fraction(0), Fraction(1), tuple(points), 0)
    ]

    while stack:
        t0, t1, section, depth = stack.pop()
        p0 = section[0]
        pn = section[-1]
        bound_squared = max(
            _squared_distance_to_segment(point, p0, pn) for point in section
        )

        if bound_squared <= tolerance_squared:
            intervals.append(
                FlattenSegment(
                    t_start=t0,
                    t_end=t1,
                    start=p0,
                    end=pn,
                    squared_bound=bound_squared,
                )
            )
            continue

        if depth == max_depth:
            raise FlattenError(
                f"error bound not met before depth limit: "
                f"interval=[{t0}, {t1}], depth={depth}, "
                f"squared geometric bound={bound_squared}, "
                f"allowed squared distance={tolerance_squared}"
            )

        left, right = _split_at_half(section)
        middle = (t0 + t1) / 2

        # Push right first so the left interval is processed first.  The two
        # halves share their middle control polygon endpoint exactly.
        stack.append((middle, t1, right, depth + 1))
        stack.append((t0, middle, left, depth + 1))

    intervals.sort(key=lambda item: item.t_start)
    _validate_partition(intervals)
    return intervals


def _as_points(
    control_points: Sequence[Sequence[RationalLike]],
) -> list[Point]:
    if not isinstance(control_points, Sequence):
        raise FlattenError("control_points must be a sequence of points")

    points: list[Point] = []
    for index, value in enumerate(control_points):
        if not isinstance(value, Sequence) or len(value) != 2:
            raise FlattenError(f"point {index} must contain exactly two coordinates")
        points.append(
            (
                _as_fraction(value[0], f"control_points[{index}][0]"),
                _as_fraction(value[1], f"control_points[{index}][1]"),
            )
        )
    return points


def _as_fraction(value: RationalLike, name: str) -> Fraction:
    if isinstance(value, bool) or not isinstance(value, (int, Fraction)):
        raise FlattenError(f"{name} must be an integer or Fraction")
    return Fraction(value)


def _squared_distance_to_segment(point: Point, start: Point, end: Point) -> Fraction:
    """Return exact squared distance from point to a closed line segment."""

    vx = end[0] - start[0]
    vy = end[1] - start[1]
    wx = point[0] - start[0]
    wy = point[1] - start[1]
    length_squared = vx * vx + vy * vy

    # Degenerate chord: distance is to the single endpoint point.
    if length_squared == 0:
        return wx * wx + wy * wy

    projection_numerator = wx * vx + wy * vy
    if projection_numerator <= 0:
        closest = start
    elif projection_numerator >= length_squared:
        closest = end
    else:
        scale = projection_numerator / length_squared
        closest = (start[0] + scale * vx, start[1] + scale * vy)

    dx = point[0] - closest[0]
    dy = point[1] - closest[1]
    return dx * dx + dy * dy


def _split_at_half(
    points: tuple[Point, ...],
) -> tuple[tuple[Point, ...], tuple[Point, ...]]:
    """Apply de Casteljau subdivision at t=1/2."""

    current = points
    left: list[Point] = [current[0]]
    right_reversed: list[Point] = [current[-1]]

    while len(current) > 1:
        following: list[Point] = []
        for first, second in zip(current, current[1:]):
            following.append(_midpoint(first, second))
        left.append(following[0])
        right_reversed.append(following[-1])
        current = tuple(following)

    return tuple(left), tuple(reversed(right_reversed))


def _midpoint(first: Point, second: Point) -> Point:
    return ((first[0] + second[0]) / 2, (first[1] + second[1]) / 2)


def _validate_partition(intervals: Iterable[FlattenSegment]) -> None:
    ordered = list(intervals)
    if not ordered:
        raise AssertionError("unreachable: flattening produced no intervals")
    if ordered[0].t_start != 0 or ordered[-1].t_end != 1:
        raise AssertionError("unreachable: flattening does not cover [0, 1]")

    for previous, current in zip(ordered, ordered[1:]):
        if previous.t_end != current.t_start:
            raise AssertionError("unreachable: parameter intervals are not continuous")
        if previous.end != current.start:
            raise AssertionError("unreachable: polyline endpoints do not continue")
