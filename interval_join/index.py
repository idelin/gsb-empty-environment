"""Centered interval-tree index for half-open [start, end) intervals."""

from __future__ import annotations

from bisect import bisect_left, bisect_right
from dataclasses import dataclass
from typing import Iterable, Iterator, Sequence

Number = int | float


@dataclass(frozen=True, slots=True)
class IndexedInterval:
    start: Number
    end: Number
    source_id: object

    def __post_init__(self) -> None:
        start = validate_endpoint(self.start, "start")
        end = validate_endpoint(self.end, "end")
        if start >= end:
            raise ValueError("interval must satisfy start < end")


@dataclass(slots=True)
class _Node:
    center: Number
    starts: list[Number]
    by_start: list[IndexedInterval]
    ends: list[Number]
    by_end: list[IndexedInterval]
    left: "_Node | None"
    right: "_Node | None"


def validate_endpoint(value: object, name: str = "endpoint") -> Number:
    """Validate a non-NaN numeric endpoint."""

    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise TypeError(f"{name} must be an integer or float")
    if value != value:
        raise ValueError(f"{name} must not be NaN")
    return value


def _partition(
    pairs: Sequence[tuple[Number, IndexedInterval]],
    center: Number,
) -> tuple[
    list[tuple[Number, IndexedInterval]],
    list[tuple[Number, IndexedInterval]],
    list[tuple[Number, IndexedInterval]],
]:
    left: list[tuple[Number, IndexedInterval]] = []
    contains: list[tuple[Number, IndexedInterval]] = []
    right: list[tuple[Number, IndexedInterval]] = []
    for pair in pairs:
        interval = pair[1]
        if interval.end <= center:
            left.append(pair)
        elif interval.start > center:
            right.append(pair)
        else:
            contains.append(pair)
    return left, contains, right


def _build(
    start_pairs: Sequence[tuple[Number, IndexedInterval]],
    end_pairs: Sequence[tuple[Number, IndexedInterval]],
) -> _Node | None:
    if not start_pairs:
        return None

    center = start_pairs[len(start_pairs) // 2][0]
    left_starts, contains, right_starts = _partition(start_pairs, center)
    left_ends = [pair for pair in end_pairs if pair[1].end <= center]
    right_ends = [pair for pair in end_pairs if pair[1].start > center]
    contains_ends = [
        pair
        for pair in end_pairs
        if pair[1].end > center and pair[1].start <= center
    ]

    return _Node(
        center=center,
        starts=[pair[0] for pair in contains],
        by_start=[pair[1] for pair in contains],
        ends=[pair[0] for pair in contains_ends],
        by_end=[pair[1] for pair in contains_ends],
        left=_build(left_starts, left_ends) if left_starts else None,
        right=_build(right_starts, right_ends) if right_starts else None,
    )


class IntervalIndex:
    """Index intervals without relying on a query engine.

    Construction is O(n log n), point lookup is O(log n + matches), and a range
    query is O(log n + matches). Result sets can be quadratic in pathological
    nested data, but non-overlapping Cartesian pairs are not examined.
    """

    __slots__ = ("_root", "size")

    def __init__(self, intervals: Iterable[IndexedInterval]) -> None:
        materialized = list(intervals)
        start_pairs = sorted(
            ((interval.start, interval) for interval in materialized),
            key=lambda pair: pair[0],
        )
        end_pairs = sorted(
            ((interval.end, interval) for interval in materialized),
            key=lambda pair: pair[0],
        )
        self._root = _build(start_pairs, end_pairs)
        self.size = len(materialized)

    def query_point(self, point: object) -> Iterator[IndexedInterval]:
        point = validate_endpoint(point, "point")
        node = self._root
        while node is not None:
            if point < node.center:
                stop = bisect_right(node.starts, point)
                yield from node.by_start[:stop]
                node = node.left
            elif point > node.center:
                first = bisect_right(node.ends, point)
                for index in range(first, len(node.by_end)):
                    yield node.by_end[index]
                node = node.right
            else:
                yield from node.by_start
                return

    def query_range(self, start: object, end: object) -> Iterator[IndexedInterval]:
        start = validate_endpoint(start, "start")
        end = validate_endpoint(end, "end")
        if start >= end or self._root is None:
            return

        def visit(node: _Node | None) -> Iterator[IndexedInterval]:
            if node is None:
                return
            if end <= node.center:
                stop = bisect_left(node.starts, end)
                yield from node.by_start[:stop]
                yield from visit(node.left)
            elif start > node.center:
                first = bisect_right(node.ends, start)
                for index in range(first, len(node.by_end)):
                    yield node.by_end[index]
                yield from visit(node.right)
            else:
                yield from node.by_start
                yield from visit(node.left)
                yield from visit(node.right)

        yield from visit(self._root)
