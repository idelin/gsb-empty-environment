from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from math import isqrt
from typing import Iterable, Sequence


Number = int | Fraction
Point = tuple[Number, Number]
Segment = tuple[Point, Point]


class Location(str, Enum):
    INSIDE = "inside"
    OUTSIDE = "outside"
    BOUNDARY = "boundary"


def _as_rational(value: object, field: str) -> Fraction:
    if isinstance(value, bool) or not isinstance(value, (int, Fraction)):
        raise TypeError(f"{field} must be an int or Fraction, got {type(value).__name__}")
    return Fraction(value)


def _normalize_point(value: Sequence[object]) -> Point:
    if len(value) != 2:
        raise ValueError("each point must contain exactly two coordinates")
    return (
        _as_rational(value[0], "x coordinate"),
        _as_rational(value[1], "y coordinate"),
    )


def _normalize_ring(points: Sequence[Sequence[object]]) -> tuple[Point, ...]:
    if not isinstance(points, Sequence):
        raise TypeError("ring points must be a sequence")
    if len(points) < 3:
        raise ValueError("a ring needs at least three distinct points")

    ring = [_normalize_point(point) for point in points]
    while len(ring) > 1 and ring[0] == ring[-1]:
        ring.pop()

    normalized: list[Point] = []
    for point in ring:
        if not normalized or point != normalized[-1]:
            normalized.append(point)

    if len(normalized) < 3:
        raise ValueError("a ring needs at least three distinct points")
    if normalized[0] == normalized[-1]:
        normalized.pop()
    return tuple(normalized)


def _twice_signed_area(points: Sequence[Point]) -> Fraction:
    area = Fraction(0)
    for index, (x1, y1) in enumerate(points):
        x2, y2 = points[(index + 1) % len(points)]
        area += x1 * y2 - x2 * y1
    return area


def _orientation(a: Point, b: Point, c: Point) -> Fraction:
    return (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])


def _strict_between(value: Number, low: Number, high: Number) -> bool:
    return min(low, high) < value < max(low, high)


def _point_on_segment(point: Point, a: Point, b: Point, inclusive: bool = True) -> bool:
    if _orientation(a, b, point) != 0:
        return False
    if inclusive:
        return (
            min(a[0], b[0]) <= point[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= point[1] <= max(a[1], b[1])
        )
    return (
        _strict_between(point[0], a[0], b[0])
        or _strict_between(point[1], a[1], b[1])
    )


def _segments_intersect(
    first: Segment,
    second: Sequence[Point],
    *,
    include_endpoints: bool,
) -> bool:
    a, b = first
    c, d = second
    o1 = _orientation(a, b, c)
    o2 = _orientation(a, b, d)
    o3 = _orientation(c, d, a)
    o4 = _orientation(c, d, b)

    if (o1 > 0 > o2 or o1 < 0 < o2) and (o3 > 0 > o4 or o3 < 0 < o4):
        return True

    if include_endpoints:
        if o1 == 0 and _point_on_segment(c, a, b):
            return True
        if o2 == 0 and _point_on_segment(d, a, b):
            return True
        if o3 == 0 and _point_on_segment(a, c, d):
            return True
        if o4 == 0 and _point_on_segment(b, c, d):
            return True
    if not include_endpoints:
        if o1 == 0 and _point_on_segment(c, a, b, inclusive=False):
            return True
        if o2 == 0 and _point_on_segment(d, a, b, inclusive=False):
            return True
        if o3 == 0 and _point_on_segment(a, c, d, inclusive=False):
            return True
        if o4 == 0 and _point_on_segment(b, c, d, inclusive=False):
            return True

    return False


def _ray_crosses(point: Point, a: Point, b: Point) -> bool:
    px, py = point
    if (a[1] > py) == (b[1] > py):
        return False
    return (_orientation(a, b, point) > 0) == (b[1] > a[1])


def _ring_edges(ring: Sequence[Point]) -> Iterable[tuple[Point, Point]]:
    for index, start in enumerate(ring):
        yield start, ring[(index + 1) % len(ring)]


@dataclass(frozen=True, slots=True)
class _RTreeNode:
    bbox: tuple[Number, Number, Number, Number]
    children: tuple["_RTreeNode", ...] = ()
    items: tuple[tuple[int, int], ...] = ()


class RTree:
    """Static STR R-tree whose items are (ring_id, edge_index) pairs."""

    def __init__(self, edges: Sequence[tuple[Segment, int, int]], node_capacity: int = 16) -> None:
        if node_capacity < 2:
            raise ValueError("node_capacity must be at least two")
        self._edge_by_key = {(ring_id, edge_id): segment for segment, ring_id, edge_id in edges}
        self.root = self._build(tuple(edges), node_capacity)

    @staticmethod
    def _item_bbox(item: tuple[Segment, int, int]) -> tuple[Number, Number, Number, Number]:
        (a, b), _, _ = item
        return min(a[0], b[0]), min(a[1], b[1]), max(a[0], b[0]), max(a[1], b[1])

    def _build(
        self,
        items: tuple[tuple[Segment, int, int], ...],
        capacity: int,
    ) -> _RTreeNode | None:
        if not items:
            return None

        sorted_items = sorted(items, key=lambda item: (self._item_bbox(item)[0], self._item_bbox(item)[2]))
        leaf_count = (len(sorted_items) + capacity - 1) // capacity
        slice_count = isqrt(leaf_count - 1) + 1
        slice_size = capacity * slice_count
        leaves: list[_RTreeNode] = []
        for x_offset in range(0, len(sorted_items), slice_size):
            x_group = sorted_items[x_offset : x_offset + slice_size]
            x_group.sort(key=lambda item: (self._item_bbox(item)[1], self._item_bbox(item)[3]))
            for offset in range(0, len(x_group), capacity):
                group = x_group[offset : offset + capacity]
                leaves.append(
                    _RTreeNode(
                        bbox=self._union_bboxes(tuple(self._item_bbox(item) for item in group)),
                        items=tuple((ring_id, edge_id) for _, ring_id, edge_id in group),
                    )
                )

        level = tuple(leaves)
        while len(level) > 1:
            next_level: list[_RTreeNode] = []
            for offset in range(0, len(level), capacity):
                group = level[offset : offset + capacity]
                next_level.append(
                    _RTreeNode(
                        bbox=self._union_bboxes(tuple(node.bbox for node in group)),
                        children=tuple(group),
                    )
                )
            level = tuple(next_level)
        return level[0]

    @staticmethod
    def _union_bboxes(bboxes: Sequence[tuple[Number, Number, Number, Number]]) -> tuple[Number, Number, Number, Number]:
        return (
            min(box[0] for box in bboxes),
            min(box[1] for box in bboxes),
            max(box[2] for box in bboxes),
            max(box[3] for box in bboxes),
        )

    @staticmethod
    def _bbox_contains(bbox: tuple[Number, Number, Number, Number], point: Point) -> bool:
        return bbox[0] <= point[0] <= bbox[2] and bbox[1] <= point[1] <= bbox[3]

    @staticmethod
    def _bbox_intersects(
        first: tuple[Number, Number, Number, Number],
        second: tuple[Number, Number, Number, Number],
    ) -> bool:
        return not (
            first[2] < second[0]
            or first[0] > second[2]
            or first[3] < second[1]
            or first[1] > second[3]
        )

    def point_candidates(self, point: Point) -> Iterable[tuple[int, int]]:
        if self.root is None:
            return ()
        return self._point_candidates(self.root, point)

    def _point_candidates(self, node: _RTreeNode, point: Point) -> Iterable[tuple[int, int]]:
        if not self._bbox_contains(node.bbox, point):
            return ()
        if node.items:
            return node.items
        candidates: list[tuple[int, int]] = []
        for child in node.children:
            candidates.extend(self._point_candidates(child, point))
        return candidates

    def right_ray_candidates(self, point: Point) -> Iterable[tuple[int, int]]:
        if self.root is None:
            return ()
        return self._right_ray_candidates(self.root, point)

    def _right_ray_candidates(self, node: _RTreeNode, point: Point) -> Iterable[tuple[int, int]]:
        px, py = point
        if node.bbox[1] >= py or node.bbox[3] < py or node.bbox[2] <= px:
            return ()
        if node.items:
            return node.items
        candidates: list[tuple[int, int]] = []
        for child in node.children:
            candidates.extend(self._right_ray_candidates(child, point))
        return candidates

    def box_candidates(self, bbox: tuple[Number, Number, Number, Number]) -> Iterable[tuple[int, int]]:
        if self.root is None:
            return ()
        return self._box_candidates(self.root, bbox)

    def _box_candidates(
        self,
        node: _RTreeNode,
        bbox: tuple[Number, Number, Number, Number],
    ) -> Iterable[tuple[int, int]]:
        if not self._bbox_intersects(node.bbox, bbox):
            return ()
        if node.items:
            return node.items
        candidates: list[tuple[int, int]] = []
        for child in node.children:
            candidates.extend(self._box_candidates(child, bbox))
        return candidates

    def segment(self, key: tuple[int, int]) -> Segment:
        return self._edge_by_key[key]

    def intersecting_keys(self, segment: Segment, bbox: tuple[Number, Number, Number, Number]) -> Iterable[tuple[int, int]]:
        for key in self.box_candidates(bbox):
            if _segments_intersect(segment, self.segment(key), include_endpoints=True):
                yield key


@dataclass(frozen=True)
class RingNode:
    """One ring and its explicitly nested children.

    Depth determines role: roots are exterior outer rings, their children are
    holes, grandchildren are islands, and so on. Ring winding order is ignored.
    """

    points: tuple[Point, ...]
    children: tuple["RingNode", ...] = ()

    def __init__(self, points: Sequence[Sequence[object]], children: Iterable["RingNode"] = ()) -> None:
        normalized = _normalize_ring(points)
        object.__setattr__(self, "points", normalized)
        object.__setattr__(self, "children", tuple(children))


@dataclass(frozen=True, slots=True)
class _CompiledRing:
    ring_id: int
    node: RingNode
    parent_id: int | None
    depth: int
    is_hole: bool


class PolygonSet:
    """Planar point-location structure for a forest of nested rings."""

    def __init__(self, roots: Iterable[RingNode], node_capacity: int = 16) -> None:
        self.rings: dict[int, _CompiledRing] = {}
        self.children: dict[int | None, list[int]] = {None: []}
        next_id = 0

        def visit(node: RingNode, parent_id: int | None, depth: int) -> int:
            nonlocal next_id
            ring_id = next_id
            next_id += 1
            compiled = _CompiledRing(
                ring_id=ring_id,
                node=node,
                parent_id=parent_id,
                depth=depth,
                is_hole=depth % 2 == 1,
            )
            if ring_id in self.rings:
                raise ValueError("internal ring id collision")
            self.rings[ring_id] = compiled
            self.children.setdefault(parent_id, []).append(ring_id)
            self.children[ring_id] = []
            for child in node.children:
                if not isinstance(child, RingNode):
                    raise TypeError("nested rings must be RingNode instances")
                visit(child, ring_id, depth + 1)
            return ring_id

        for root in roots:
            if not isinstance(root, RingNode):
                raise TypeError("roots must be RingNode instances")
            root_id = visit(root, None, 0)
            self.children[None].append(root_id)

        for compiled in self.rings.values():
            self._validate_simple_ring(compiled)
        self.index = self._build_index(node_capacity)
        self._validate_global_topology()

    def _validate_simple_ring(self, compiled: _CompiledRing) -> None:
        ring = compiled.node.points
        if _twice_signed_area(ring) == 0:
            raise ValueError(f"ring {compiled.ring_id} has zero area")

        point_count: dict[Point, int] = {}
        for point in ring:
            point_count[point] = point_count.get(point, 0) + 1
        duplicated = [point for point, count in point_count.items() if count > 1]
        if duplicated:
            raise ValueError(f"ring {compiled.ring_id} contains a repeated non-adjacent vertex")

        edge_count = len(ring)
        for edge_id in range(edge_count):
            a = ring[edge_id]
            b = ring[(edge_id + 1) % edge_count]
            for other_id in range(edge_id + 1, edge_count):
                c = ring[other_id]
                d = ring[(other_id + 1) % edge_count]
                adjacent = (
                    other_id == edge_id + 1
                    or (edge_id == 0 and other_id == edge_count - 1)
                )
                intersects = _segments_intersect(
                    (a, b),
                    (c, d),
                    include_endpoints=not adjacent,
                )
                if intersects:
                    raise ValueError(f"ring {compiled.ring_id} is self-intersecting")

    def _build_index(self, node_capacity: int) -> RTree:
        edges: list[tuple[Segment, int, int]] = []
        for compiled in self.rings.values():
            for edge_id, edge in enumerate(_ring_edges(compiled.node.points)):
                edges.append((edge, compiled.ring_id, edge_id))
        return RTree(edges, node_capacity=node_capacity)

    def _validate_global_topology(self) -> None:
        for ring_id, compiled in self.rings.items():
            for edge_id, segment in enumerate(_ring_edges(compiled.node.points)):
                a, b = segment
                bbox = (
                    min(a[0], b[0]),
                    min(a[1], b[1]),
                    max(a[0], b[0]),
                    max(a[1], b[1]),
                )
                for other_ring_id, _ in self.index.intersecting_keys(segment, bbox):
                    if other_ring_id != ring_id:
                        raise ValueError(
                            f"rings {ring_id} and {other_ring_id} touch or cross"
                        )

        for ring_id, compiled in self.rings.items():
            representative = compiled.node.points[0]
            actual_ancestors = set()
            for other_id in self.rings:
                if other_id == ring_id:
                    continue
                if self._point_in_ring_only(representative, other_id):
                    actual_ancestors.add(other_id)

            expected_ancestors = set()
            parent_id = compiled.parent_id
            while parent_id is not None:
                expected_ancestors.add(parent_id)
                parent_id = self.rings[parent_id].parent_id

            if actual_ancestors != expected_ancestors:
                raise ValueError(
                    f"ring {ring_id} nesting does not match its explicit hierarchy"
                )

    def _point_in_ring_only(self, point: Point, ring_id: int) -> bool:
        ring = self.rings[ring_id].node.points
        return sum(1 for edge in _ring_edges(ring) if _ray_crosses(point, *edge)) % 2 == 1

    def locate(self, point: Sequence[object]) -> Location:
        normalized = _normalize_point(point)

        boundary_rings = self._boundary_rings(normalized)
        if boundary_rings:
            return Location.BOUNDARY

        if not self.children[None]:
            return Location.OUTSIDE

        crossings = self._crossing_counts(normalized)
        if not any(count % 2 for count in crossings.values()):
            return Location.OUTSIDE

        if any(self._inside_root(root_id, normalized, crossings) for root_id in self.children[None]):
            return Location.INSIDE
        return Location.OUTSIDE

    def locate_all(self, points: Iterable[Sequence[object]]) -> list[Location]:
        return [self.locate(point) for point in points]

    def _boundary_rings(self, point: Point) -> set[int]:
        result: set[int] = set()
        for ring_id, edge_id in self.index.point_candidates(point):
            a, b = self.index.segment((ring_id, edge_id))
            if _point_on_segment(point, a, b):
                result.add(ring_id)
        return result

    def _crossing_counts(self, point: Point) -> dict[int, int]:
        counts: dict[int, int] = {}
        for ring_id, edge_id in self.index.right_ray_candidates(point):
            a, b = self.index.segment((ring_id, edge_id))
            if _ray_crosses(point, a, b):
                counts[ring_id] = counts.get(ring_id, 0) + 1
        return counts

    def _inside_root(self, ring_id: int, point: Point, crossings: dict[int, int]) -> bool:
        if crossings.get(ring_id, 0) % 2 == 0:
            return False
        for child_id in self.children[ring_id]:
            if self._inside_hole(child_id, point, crossings):
                return False
        return True

    def _inside_hole(self, ring_id: int, point: Point, crossings: dict[int, int]) -> bool:
        if crossings.get(ring_id, 0) % 2 == 0:
            return False
        for child_id in self.children[ring_id]:
            if self._inside_root(child_id, point, crossings):
                return False
        return True


class Polygon(PolygonSet):
    """Convenience class for one exterior ring with directly nested holes."""

    def __init__(
        self,
        outer: Sequence[Sequence[object]],
        holes: Iterable[Sequence[Sequence[object]]] = (),
        node_capacity: int = 16,
    ) -> None:
        root = RingNode(outer, tuple(RingNode(hole) for hole in holes))
        super().__init__([root], node_capacity=node_capacity)
