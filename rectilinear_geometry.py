from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from numbers import Rational
from typing import Iterable


Point = tuple[Fraction, Fraction]
Ring = tuple[Point, ...]


def _as_fraction(value: object) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value, 1)
    if isinstance(value, Rational):
        return Fraction(value.numerator, value.denominator)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"exact rational coordinate expected, got {type(value).__name__}")


@dataclass(frozen=True)
class Rectangle:
    x0: Fraction
    y0: Fraction
    x1: Fraction
    y1: Fraction

    def __init__(self, x0: object, y0: object, x1: object, y1: object) -> None:
        ax, ay, bx, by = (_as_fraction(x0), _as_fraction(y0), _as_fraction(x1), _as_fraction(y1))
        if ax > bx:
            ax, bx = bx, ax
        if ay > by:
            ay, by = by, ay
        object.__setattr__(self, "x0", ax)
        object.__setattr__(self, "y0", ay)
        object.__setattr__(self, "x1", bx)
        object.__setattr__(self, "y1", by)

    @classmethod
    def from_value(cls, value: Iterable[object]) -> "Rectangle":
        if isinstance(value, Rectangle):
            return value
        items = tuple(value)
        if len(items) != 4:
            raise ValueError("a rectangle requires four coordinates x0, y0, x1, y1")
        return cls(*items)

    @property
    def width(self) -> Fraction:
        return self.x1 - self.x0

    @property
    def height(self) -> Fraction:
        return self.y1 - self.y0

    @property
    def is_degenerate(self) -> bool:
        return self.width == 0 or self.height == 0


@dataclass(frozen=True)
class GeometryResult:
    area: Fraction
    perimeter: Fraction
    outer_perimeter: Fraction
    outer_rings: tuple[Ring, ...]
    hole_rings: tuple[Ring, ...]


def rectangle(value: Iterable[object]) -> Rectangle:
    return Rectangle.from_value(value)


def _ring_signed_area(ring: Ring) -> Fraction:
    total = Fraction(0)
    for index, (x, y) in enumerate(ring):
        nx, ny = ring[(index + 1) % len(ring)]
        total += x * ny - y * nx
    return total / 2


def _ring_length(ring: Ring) -> Fraction:
    total = Fraction(0)
    for index, (x, y) in enumerate(ring):
        nx, ny = ring[(index + 1) % len(ring)]
        total += abs(nx - x) + abs(ny - y)
    return total


def _normalize_ring(ring: Ring, clockwise: bool = False) -> Ring:
    signed_area = _ring_signed_area(ring)
    if signed_area == 0:
        raise RuntimeError("degenerate boundary ring")
    points = list(ring)
    wants_negative = clockwise
    if (signed_area < 0) != wants_negative:
        points.reverse()
    start = min(range(len(points)), key=lambda index: points[index])
    points = points[start:] + points[:start]
    return tuple(points)


def _remove_collinear_points(ring: Ring) -> Ring:
    points = list(ring)
    changed = True
    while changed and len(points) > 3:
        changed = False
        next_points: list[Point] = []
        count = len(points)
        for index, point in enumerate(points):
            previous = points[(index - 1) % count]
            following = points[(index + 1) % count]
            vertical = previous[0] == point[0] == following[0]
            horizontal = previous[1] == point[1] == following[1]
            if vertical or horizontal:
                changed = True
            else:
                next_points.append(point)
        points = next_points
    return tuple(points)


def analyze_rectangles(values: Iterable[Iterable[object]]) -> GeometryResult:
    rectangles = [Rectangle.from_value(value) for value in values]
    rectangles = [item for item in rectangles if not item.is_degenerate]

    if not rectangles:
        return GeometryResult(Fraction(0), Fraction(0), Fraction(0), (), ())

    xs = sorted({item.x0 for item in rectangles} | {item.x1 for item in rectangles})
    ys = sorted({item.y0 for item in rectangles} | {item.y1 for item in rectangles})
    x_index = {value: index for index, value in enumerate(xs)}
    y_index = {value: index for index, value in enumerate(ys)}
    x_cells = len(xs) - 1
    y_cells = len(ys) - 1

    coverage = [0 for _ in range(x_cells)]
    for item in rectangles:
        xa = x_index[item.x0]
        xb = x_index[item.x1]
        ya = y_index[item.y0]
        yb = y_index[item.y1]
        y_mask = ((1 << (yb - ya)) - 1) << ya
        for xi in range(xa, xb):
            coverage[xi] |= y_mask

    inside = [bytearray(y_cells) for _ in range(x_cells)]
    area = Fraction(0)
    for xi, mask in enumerate(coverage):
        cell_width = xs[xi + 1] - xs[xi]
        for yi in range(y_cells):
            if (mask >> yi) & 1:
                inside[xi][yi] = 1
                area += cell_width * (ys[yi + 1] - ys[yi])

    edges: set[tuple[Point, Point]] = set()

    for xi in range(x_cells):
        for yi in range(len(ys)):
            below = inside[xi][yi - 1] if yi > 0 else 0
            above = inside[xi][yi] if yi < y_cells else 0
            if below == above:
                continue
            left_point = (xs[xi], ys[yi])
            right_point = (xs[xi + 1], ys[yi])
            if above:
                edges.add((left_point, right_point))
            else:
                edges.add((right_point, left_point))

    for xi in range(len(xs)):
        for yi in range(y_cells):
            left_side = inside[xi - 1][yi] if xi > 0 else 0
            right_side = inside[xi][yi] if xi < x_cells else 0
            if left_side == right_side:
                continue
            bottom_point = (xs[xi], ys[yi])
            top_point = (xs[xi], ys[yi + 1])
            if left_side:
                edges.add((bottom_point, top_point))
            else:
                edges.add((top_point, bottom_point))

    perimeter = sum(
        (abs(edge[1][0] - edge[0][0]) + abs(edge[1][1] - edge[0][1]) for edge in edges),
        Fraction(0),
    )

    continuation: dict[tuple[Point, Point], tuple[Point, Point]] = {}
    ray_neighbors = ((1, 0), (0, 1), (-1, 0), (0, -1))

    for xi in range(len(xs)):
        for yi in range(len(ys)):
            vertex = (xs[xi], ys[yi])
            northeast = xi < x_cells and yi < y_cells and bool(inside[xi][yi])
            northwest = xi > 0 and yi < y_cells and bool(inside[xi - 1][yi])
            southwest = xi > 0 and yi > 0 and bool(inside[xi - 1][yi - 1])
            southeast = xi < x_cells and yi > 0 and bool(inside[xi][yi - 1])
            sectors = (northeast, northwest, southwest, southeast)

            for start in range(4):
                if not sectors[start] or sectors[(start - 1) % 4]:
                    continue
                length = 1
                while sectors[(start + length) % 4] and length < 4:
                    length += 1
                if length == 4:
                    continue

                ray_pair = (start, (start + length) % 4)
                incoming: list[tuple[Point, Point]] = []
                outgoing: list[tuple[Point, Point]] = []

                for ray in ray_pair:
                    dx, dy = ray_neighbors[ray]
                    nx = xi + dx
                    ny = yi + dy
                    neighbor = (xs[nx], ys[ny])
                    outward = (vertex, neighbor)
                    inward = (neighbor, vertex)
                    if outward in edges:
                        outgoing.append(outward)
                    elif inward in edges:
                        incoming.append(inward)

                if len(incoming) != 1 or len(outgoing) != 1:
                    raise RuntimeError("non-manifold rectilinear boundary")
                continuation[incoming[0]] = outgoing[0]

    remaining = set(edges)
    raw_rings: list[Ring] = []
    while remaining:
        start_edge = next(iter(remaining))
        edge = start_edge
        points: list[Point] = []
        while True:
            points.append(edge[0])
            remaining.remove(edge)
            edge = continuation[edge]
            if edge == start_edge:
                break
        raw_rings.append(_remove_collinear_points(tuple(points)))

    outer_rings: list[Ring] = []
    hole_rings: list[Ring] = []
    for raw_ring in raw_rings:
        signed_area = _ring_signed_area(raw_ring)
        if signed_area > 0:
            outer_rings.append(_normalize_ring(raw_ring))
        else:
            hole_rings.append(_normalize_ring(raw_ring, clockwise=True))

    outer_rings.sort()
    hole_rings.sort()
    outer_perimeter = sum((_ring_length(ring) for ring in outer_rings), Fraction(0))

    return GeometryResult(
        area=area,
        perimeter=perimeter,
        outer_perimeter=outer_perimeter,
        outer_rings=tuple(outer_rings),
        hole_rings=tuple(hole_rings),
    )
