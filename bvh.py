"""Exact, dynamic axis-aligned bounding volume hierarchy.

Coordinates are kept as int or Fraction.  All geometric comparisons therefore
use exact arithmetic; touching boxes are intersecting and a degenerate box is
just a box with one or more zero-width axes.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from heapq import heapreplace, heappop, heappush
from typing import Any, Iterator, Sequence


Number = int | Fraction
Box = Sequence[tuple[Number, Number]]
Point = Sequence[Number]


@dataclass(frozen=True, slots=True)
class Bounds:
    """Immutable axis-aligned box."""

    intervals: tuple[tuple[Fraction, Fraction], ...]

    @property
    def dimension(self) -> int:
        return len(self.intervals)

    def intersects(self, other: Bounds) -> bool:
        return all(
            lower <= other_upper and other_lower <= upper
            for (lower, upper), (other_lower, other_upper) in zip(
                self.intervals, other.intervals
            )
        )

    def union_with(self, other: Bounds) -> Bounds:
        return Bounds(
            tuple(
                (min(lower, other_lower), max(upper, other_upper))
                for (lower, upper), (other_lower, other_upper) in zip(
                    self.intervals, other.intervals
                )
            )
        )

    def squared_distance_to(self, point: Sequence[Fraction]) -> Fraction:
        total = Fraction(0)
        for value, (lower, upper) in zip(point, self.intervals):
            if value < lower:
                total += (lower - value) ** 2
            elif value > upper:
                total += (value - upper) ** 2
        return total

    def __iter__(self) -> Iterator[tuple[Fraction, Fraction]]:
        return iter(self.intervals)

    def __len__(self) -> int:
        return len(self.intervals)


def _is_exact_number(value: Any) -> bool:
    return isinstance(value, (int, Fraction)) and not isinstance(value, bool)


def _as_fraction(value: Any) -> Fraction:
    if not _is_exact_number(value):
        raise TypeError("coordinates must be int or fractions.Fraction")
    return value if isinstance(value, Fraction) else Fraction(value)


def _is_sequence(value: Any) -> bool:
    return (
        not isinstance(value, (str, bytes, bytearray, dict))
        and hasattr(value, "__len__")
        and hasattr(value, "__getitem__")
    )


def make_bounds(value: Any, dimension: int | None = None) -> Bounds:
    """Convert supported point/box descriptions to a validated Bounds."""
    if isinstance(value, Bounds):
        box = value
        if dimension is not None and box.dimension != dimension:
            raise ValueError("bounding box has the wrong dimension")
        return box
    if not _is_sequence(value):
        raise TypeError("bounding box must be a sequence")

    scalar = len(value) > 0 and not _is_sequence(value[0])
    intervals: list[tuple[Fraction, Fraction]] = []
    if scalar:
        if dimension not in (None, 1):
            raise ValueError("a scalar box is only valid in dimension one")
        if len(value) != 2:
            raise ValueError("one-dimensional box must contain two endpoints")
        items = [value]
    else:
        if dimension is not None and len(value) != dimension:
            raise ValueError("bounding box has the wrong dimension")
        items = list(value)

    for interval in items:
        if not _is_sequence(interval) or len(interval) != 2:
            raise ValueError("each axis interval must contain two endpoints")
        lower = _as_fraction(interval[0])
        upper = _as_fraction(interval[1])
        if lower > upper:
            raise ValueError("interval lower endpoint must not exceed upper endpoint")
        intervals.append((lower, upper))
    if not intervals:
        raise ValueError("bounding box must contain at least one axis")
    return Bounds(tuple(intervals))


def make_point(value: Any, dimension: int) -> tuple[Fraction, ...]:
    """Validate and convert a query point."""
    if not _is_sequence(value) or len(value) != dimension:
        raise ValueError(f"point must contain exactly {dimension} coordinates")
    point = tuple(_as_fraction(item) for item in value)
    return point


def _measure_terms(bounds: Bounds) -> tuple[Fraction, ...]:
    """Elementary symmetric polynomials in the positive side lengths.

    In three dimensions this is (perimeter contribution, surface area,
    volume), without the conventional scalar factors.  Lower-dimensional
    terms give exact, non-epsilon distinctions when boxes are flat or points.
    """
    terms = [Fraction(0)] * bounds.dimension
    for lower, upper in bounds.intervals:
        width = upper - lower
        updated = terms.copy()
        for degree, value in enumerate(terms[:-1]):
            updated[degree + 1] += value * width
        updated[0] += width
        terms = updated
    return tuple(terms)


def _zero_terms(dimension: int) -> tuple[Fraction, ...]:
    return tuple(Fraction(0) for _ in range(dimension))


def _add_terms(
    left: tuple[Fraction, ...], right: tuple[Fraction, ...]
) -> tuple[Fraction, ...]:
    return tuple(a + b for a, b in zip(left, right))


def _sub_terms(
    left: tuple[Fraction, ...], right: tuple[Fraction, ...]
) -> tuple[Fraction, ...]:
    return tuple(a - b for a, b in zip(left, right))


class _Node:
    __slots__ = (
        "node_id",
        "bounds",
        "cost",
        "item",
        "left",
        "right",
        "parent",
        "height",
        "leaf_count",
    )

    def __init__(
        self,
        node_id: int,
        bounds: Bounds,
        *,
        item: Any = None,
        left: "_Node | None" = None,
        right: "_Node | None" = None,
    ) -> None:
        self.node_id = node_id
        self.bounds = bounds
        if left is None:
            self.cost = _zero_terms(bounds.dimension)
        else:
            self.cost = _measure_terms(bounds)
        self.item = item
        self.left = left
        self.right = right
        self.parent: _Node | None = None
        self.height = 0 if left is None else max(left.height, right.height) + 1
        self.leaf_count = 1 if left is None else left.leaf_count + right.leaf_count
        if left is not None:
            left.parent = self
            right.parent = self

    @property
    def is_leaf(self) -> bool:
        return self.left is None


class BVHIndex:
    """Dynamic BVH over integer or exact rational axis-aligned boxes."""

    def __init__(self, dimension: int = 2) -> None:
        if (
            not isinstance(dimension, int)
            or isinstance(dimension, bool)
            or dimension <= 0
        ):
            raise ValueError("dimension must be a positive integer")
        self._dimension = dimension
        self._root: _Node | None = None
        self._leaves: dict[Any, _Node] = {}
        self._next_node_id = 0

    @property
    def dimension(self) -> int:
        return self._dimension

    def __len__(self) -> int:
        return len(self._leaves)

    def __contains__(self, item: Any) -> bool:
        return item in self._leaves

    def __iter__(self) -> Iterator[Any]:
        return iter(self.ids())

    def ids(self) -> list[Any]:
        return sorted(self._leaves)

    def bounds(self, item: Any) -> Bounds:
        try:
            return self._leaves[item].bounds
        except KeyError:
            raise KeyError(item) from None

    def insert(self, item: Any, box: Any) -> None:
        """Insert one item; raise KeyError if its ID already exists."""
        if item in self._leaves:
            raise KeyError(f"duplicate item id: {item!r}")
        bounds = make_bounds(box, self._dimension)
        self._insert_leaf(self._new_leaf(bounds, item))

    def remove(self, item: Any) -> Bounds:
        """Remove one item and return its old bounds."""
        leaf = self._leaves.get(item)
        if leaf is None:
            raise KeyError(item)
        old_bounds = leaf.bounds
        self._detach_leaf(leaf)
        del self._leaves[item]
        self._rebalance_if_tall()
        return old_bounds

    def discard(self, item: Any) -> Bounds | None:
        """Remove one item, or return None when it is absent."""
        leaf = self._leaves.get(item)
        if leaf is None:
            return None
        old_bounds = leaf.bounds
        self._detach_leaf(leaf)
        del self._leaves[item]
        self._rebalance_if_tall()
        return old_bounds

    def move(self, item: Any, box: Any) -> None:
        """Change an item's region; equal boxes are a defined no-op."""
        leaf = self._leaves.get(item)
        if leaf is None:
            raise KeyError(item)
        bounds = make_bounds(box, self._dimension)
        if bounds == leaf.bounds:
            return
        self._leaves.pop(item)
        self._detach_leaf(leaf)
        leaf.bounds = bounds
        leaf.cost = _zero_terms(self._dimension)
        leaf.parent = None
        leaf.height = 0
        leaf.leaf_count = 1
        self._rebalance_if_tall()
        self._insert_leaf(leaf)

    def intersects(self, box: Any) -> list[Any]:
        """Return IDs whose boxes intersect the query; touching counts."""
        query = make_bounds(box, self._dimension)
        if self._root is None:
            return []
        matches: list[Any] = []
        stack = [self._root]
        while stack:
            node = stack.pop()
            if not query.intersects(node.bounds):
                continue
            if node.is_leaf:
                matches.append(node.item)
            else:
                stack.append(node.right)
                stack.append(node.left)
        return sorted(matches)

    def query_intersection(self, box: Any) -> list[Any]:
        return self.intersects(box)

    def nearest(self, point: Any) -> Any:
        """Return the closest item ID, or None for an empty tree."""
        result = self.nearest_with_distance(point)
        return None if result is None else result[0]

    def nearest_with_distance(self, point: Any) -> tuple[Any, Fraction] | None:
        """Return the closest ``(id, squared_distance)`` pair."""
        rows = self.nearest_k(point, 1)
        return rows[0] if rows else None

    def nearest_k(self, point: Any, k: int) -> list[tuple[Any, Fraction]]:
        """Return up to k closest ``(id, squared_distance)`` pairs."""
        if not isinstance(k, int) or isinstance(k, bool) or k <= 0:
            raise ValueError("k must be a positive integer")
        query_point = make_point(point, self._dimension)
        if self._root is None:
            return []

        candidates: list[tuple[Fraction, int, _Node]] = []
        root_distance = self._root.bounds.squared_distance_to(query_point)
        heappush(candidates, (root_distance, self._root.node_id, self._root))
        selected: list[_WorstEntry] = []

        while candidates:
            node_distance, _, node = heappop(candidates)
            if len(selected) >= k and node_distance > selected[0].distance:
                break
            if node.is_leaf:
                entry = _WorstEntry(node_distance, node.item)
                if len(selected) < k:
                    heappush(selected, entry)
                elif selected[0] < entry:
                    heapreplace(selected, entry)
                continue

            for child in (node.right, node.left):
                child_distance = child.bounds.squared_distance_to(query_point)
                if len(selected) >= k and child_distance > selected[0].distance:
                    continue
                heappush(candidates, (child_distance, child.node_id, child))

        rows = [(entry.item, entry.distance) for entry in selected]
        return sorted(rows, key=lambda row: (row[1], row[0]))

    def check_invariants(self) -> None:
        """Validate internal structure; useful for tests and debugging."""
        if self._root is None:
            if self._leaves:
                raise AssertionError("leaf map is not empty for an empty tree")
            return
        seen: set[int] = set()
        stack = [(self._root, None)]
        leaf_count = 0
        while stack:
            node, parent = stack.pop()
            if node.node_id in seen:
                raise AssertionError("node appears more than once")
            seen.add(node.node_id)
            if node.parent is not parent:
                raise AssertionError("incorrect parent pointer")
            if node.bounds.dimension != self._dimension:
                raise AssertionError("incorrect node dimension")
            if node.is_leaf:
                leaf_count += 1
                if self._leaves.get(node.item) is not node:
                    raise AssertionError("leaf is not mapped by its item ID")
                if node.height != 0 or node.leaf_count != 1:
                    raise AssertionError("leaf metadata is incorrect")
                continue
            if node.left is None or node.right is None:
                raise AssertionError("internal node must have two children")
            expected = node.left.bounds.union_with(node.right.bounds)
            if node.bounds != expected:
                raise AssertionError("node bounds do not equal child bounds union")
            if node.cost != _measure_terms(node.bounds):
                raise AssertionError("node cost is stale")
            expected_height = max(node.left.height, node.right.height) + 1
            expected_leaves = node.left.leaf_count + node.right.leaf_count
            if node.height != expected_height or node.leaf_count != expected_leaves:
                raise AssertionError("internal metadata is stale")
            stack.append((node.right, node))
            stack.append((node.left, node))
        if leaf_count != len(self._leaves):
            raise AssertionError("leaf count and leaf map disagree")

    def _new_leaf(self, bounds: Bounds, item: Any) -> _Node:
        node = _Node(self._next_node_id, bounds, item=item)
        self._next_node_id += 1
        return node

    def _new_internal(self, left: _Node, right: _Node) -> _Node:
        bounds = left.bounds.union_with(right.bounds)
        node = _Node(self._next_node_id, bounds, left=left, right=right)
        self._next_node_id += 1
        return node

    def _insert_leaf(self, leaf: _Node) -> None:
        self._leaves[leaf.item] = leaf
        if self._root is None:
            self._root = leaf
            return

        sibling, bounds, cost = self._choose_sibling(leaf)
        parent = sibling.parent
        new_parent = _Node(self._next_node_id, bounds, left=sibling, right=leaf)
        self._next_node_id += 1
        new_parent.cost = cost
        new_parent.parent = parent
        leaf.parent = new_parent
        sibling.parent = new_parent

        if parent is None:
            self._root = new_parent
        elif parent.left is sibling:
            parent.left = new_parent
        else:
            parent.right = new_parent

        self._refit_ancestors(new_parent)
        self._rebalance_if_tall()

    def _choose_sibling(
        self, leaf: _Node
    ) -> tuple[_Node, Bounds, tuple[Fraction, ...]]:
        assert self._root is not None
        root_bounds = leaf.bounds.union_with(self._root.bounds)
        root_cost = _measure_terms(root_bounds)
        best_score = _sub_terms(root_cost, self._root.cost)
        best_node = self._root
        best_bounds = root_bounds
        best_cost = root_cost

        stack: list[tuple[_Node, tuple[Fraction, ...]]] = [
            (self._root, _zero_terms(self._dimension))
        ]
        while stack:
            candidate, inherited = stack.pop()
            combined = leaf.bounds.union_with(candidate.bounds)
            combined_cost = _measure_terms(combined)
            direct_delta = _sub_terms(combined_cost, candidate.cost)
            score = _add_terms(inherited, direct_delta)
            key = (score, candidate.height, candidate.node_id)
            if key < (best_score, best_node.height, best_node.node_id):
                best_score = score
                best_node = candidate
                best_bounds = combined
                best_cost = combined_cost

            if not candidate.is_leaf:
                assert candidate.left is not None and candidate.right is not None
                enlarged_cost = _measure_terms(
                    leaf.bounds.union_with(candidate.bounds)
                )
                child_inherited = _add_terms(
                    inherited, _sub_terms(enlarged_cost, candidate.cost)
                )
                if child_inherited <= best_score:
                    stack.append((candidate.right, child_inherited))
                if child_inherited <= best_score:
                    stack.append((candidate.left, child_inherited))

        return best_node, best_bounds, best_cost

    def _refit_ancestors(self, start: _Node) -> None:
        node = start.parent
        while node is not None:
            assert node.left is not None and node.right is not None
            node.bounds = node.left.bounds.union_with(node.right.bounds)
            node.cost = _measure_terms(node.bounds)
            node.height = max(node.left.height, node.right.height) + 1
            node.leaf_count = node.left.leaf_count + node.right.leaf_count
            node = node.parent

    def _detach_leaf(self, leaf: _Node) -> None:
        parent = leaf.parent
        if parent is None:
            self._root = None
            return

        sibling = parent.right if parent.left is leaf else parent.left
        grandparent = parent.parent
        sibling.parent = grandparent
        if grandparent is None:
            self._root = sibling
        elif grandparent.left is parent:
            grandparent.left = sibling
        else:
            grandparent.right = sibling
        self._refit_ancestors(sibling)

    def _rebalance_if_tall(self) -> None:
        count = len(self._leaves)
        if self._root is None:
            return
        limit = 3 * count.bit_length() + 4
        if self._root.height <= limit:
            return
        self._rebuild()

    def _rebuild(self) -> None:
        leaves = list(self._leaves.values())
        if not leaves:
            self._root = None
            return
        self._root = self._build_nodes(leaves)

    def _build_nodes(self, leaves: list[_Node]) -> _Node:
        if len(leaves) == 1:
            node = leaves[0]
            node.parent = None
            return node

        total = leaves[0].bounds
        for leaf in leaves[1:]:
            total = total.union_with(leaf.bounds)
        axis = min(
            range(self._dimension),
            key=lambda axis_index: (
                -(total.intervals[axis_index][1] - total.intervals[axis_index][0]),
                axis_index,
            ),
        )
        leaves.sort(
            key=lambda leaf: (
                leaf.bounds.intervals[axis][0] + leaf.bounds.intervals[axis][1],
                leaf.item,
                leaf.node_id,
            )
        )
        middle = len(leaves) // 2
        return self._new_internal(
            self._build_nodes(leaves[:middle]),
            self._build_nodes(leaves[middle:]),
        )


@dataclass(frozen=True, slots=True)
class _WorstEntry:
    """Max-heap entry: larger distance is worse, then larger ID is worse."""

    distance: Fraction
    item: Any

    def __lt__(self, other: "_WorstEntry") -> bool:
        if self.distance != other.distance:
            return self.distance > other.distance
        return self.item > other.item
