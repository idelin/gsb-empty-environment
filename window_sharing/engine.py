"""Local, pure-Python execution shared by the demo and the tests.

The executor follows the sharing plan: each :class:`SortGroup` sorts every
partition *once* and all member windows read that same ordered list.  Windows
keep independent ROWS frames and therefore independent result arrays - a sort
is shared, results are not.

A module-level/injected counter records the number of physical ``sorted``
calls ("actual sort count"), which is exactly one per (sort shape, partition).
"""

from __future__ import annotations

from dataclasses import dataclass
from functools import cmp_to_key
from typing import Any

from .model import (
    CURRENT_ROW,
    FOLLOWING,
    PRECEDING,
    UNBOUNDED_FOLLOWING,
    UNBOUNDED_PRECEDING,
    Frame,
    FrameBound,
    OrderKey,
    WindowSpec,
)
from .planner import SharePlan, build_share_plan

Row = dict[str, Any]


class SortCounter:
    """Counts real sort invocations performed by the executor."""

    def __init__(self) -> None:
        self.count = 0

    def tick(self) -> None:
        self.count += 1


def _compare(left: Any, right: Any) -> int:
    """Three-way compare with SQL NULL semantics handled by the caller."""
    if left < right:
        return -1
    if left > right:
        return 1
    return 0


def _key_comparator(order_by: tuple[OrderKey, ...]):
    def comparator(a: tuple[int, Row], b: tuple[int, Row]) -> int:
        _, left_row = a
        _, right_row = b
        for key in order_by:
            left_value = left_row[key.column]
            right_value = right_row[key.column]
            if left_value is None or right_value is None:
                if left_value is None and right_value is None:
                    continue  # both NULL: peers on this key
                left_is_null = left_value is None
                nulls_first = key.effective_nulls_first()
                # -1 sorts earlier. NULL sorts first iff nulls_first.
                if left_is_null:
                    return -1 if nulls_first else 1
                return 1 if nulls_first else -1
            direction = _compare(left_value, right_value)
            if direction:
                return direction if key.ascending else -direction
        # Stable tie-break on original input position: deterministic row_number.
        if a[0] < b[0]:
            return -1
        if a[0] > b[0]:
            return 1
        return 0

    return comparator


def partition_rows(rows: list[Row], partition_by: tuple[str, ...]) -> list[list[tuple[int, Row]]]:
    """Split rows into partitions, preserving first-seen partition order."""
    buckets: dict[tuple[Any, ...], list[tuple[int, Row]]] = {}
    for index, row in enumerate(rows):
        part_key = tuple(row[column] for column in partition_by)
        buckets.setdefault(part_key, []).append((index, row))
    return list(buckets.values())


def partition_key_counts(rows: list[Row], plan: SharePlan) -> dict[tuple[str, ...], int]:
    """For each distinct PARTITION BY shape in the plan, number of partitions."""
    shapes = {group.partition_by for group in plan.groups}
    return {
        shape: len({tuple(row[column] for column in shape) for row in rows})
        for shape in shapes
    }


def _bound_index(bound: FrameBound, position: int, size: int) -> int:
    if bound.kind == UNBOUNDED_PRECEDING:
        return 0
    if bound.kind == UNBOUNDED_FOLLOWING:
        return size - 1
    if bound.kind == CURRENT_ROW:
        return position
    if bound.kind == PRECEDING:
        return max(0, position - (bound.offset or 0))
    if bound.kind == FOLLOWING:
        return min(size - 1, position + (bound.offset or 0))
    raise ValueError(f"unknown frame bound: {bound.kind}")  # pragma: no cover


def frame_slice(frame: Frame, position: int, size: int) -> tuple[int, int]:
    """Return the clamped ``[start, end)`` Python slice of the frame."""
    start = _bound_index(frame.start, position, size)
    end = _bound_index(frame.end, position, size)
    if start > end:  # e.g. 1 PRECEDING on the row before a truncated window
        return position, position
    return start, end + 1


def _aggregate(func: str, rows: list[Row], column: str | None) -> Any:
    if not rows:
        return None
    if func == "count":
        if column is None:
            return len(rows)
        return sum(1 for row in rows if row[column] is not None)
    values: list[Any]
    if column is None:
        raise ValueError(f"{func} needs a value column")
    values = [row[column] for row in rows if row[column] is not None]
    if func == "sum":
        return sum(values) if values else None
    if func == "avg":
        return sum(values) / len(values) if values else None
    raise ValueError(f"not an aggregate: {func}")  # pragma: no cover


@dataclass
class WindowResult:
    name: str
    # value aligned with the *input* row order
    values: list[Any]


def execute_windows(
    rows: list[Row],
    windows: list[WindowSpec] | dict[str, WindowSpec],
    counter: SortCounter | None = None,
) -> tuple[list[WindowResult], SharePlan]:
    """Execute all windows using one shared sort per (shape, partition)."""

    plan = build_share_plan(windows)
    counter = counter or SortCounter()
    results = {
        window.name: [None] * len(rows)
        for window in plan.windows.values()
    }

    for group in plan.groups:
        partitions = partition_rows(rows, group.partition_by)
        comparator = _key_comparator(group.order_by)
        for indexed_partition in partitions:
            counter.tick()
            ordered = sorted(indexed_partition, key=cmp_to_key(comparator))

            for window_name in group.window_names:
                window = plan.windows[window_name]
                _evaluate_window(window, ordered, results[window_name])

    ordered_results = [
        WindowResult(name=window.name, values=results[window.name])
        for window in sorted(plan.windows.values(), key=lambda item: item.name)
    ]
    return ordered_results, plan


def _evaluate_window(
    window: WindowSpec,
    ordered: list[tuple[int, Row]],
    output: list[Any],
) -> None:
    size = len(ordered)
    rank_value = 0
    dense_value = 0
    previous_sort_values: tuple | None = None

    for position, (original_index, row) in enumerate(ordered):
        func = window.func
        if func == "row_number":
            output[original_index] = position + 1
            continue

        sort_values = tuple(
            (None if row[key.column] is None else row[key.column]) for key in window.order_by
        )
        if func in ("rank", "dense_rank"):
            if position == 0 or sort_values != previous_sort_values:
                if func == "rank":
                    rank_value = position + 1
                else:
                    dense_value += 1
            previous_sort_values = sort_values
            output[original_index] = rank_value if func == "rank" else dense_value
            continue

        frame = window.effective_frame()
        start, end = frame_slice(frame, position, size)
        frame_rows = [item[1] for item in ordered[start:end]]
        output[original_index] = _aggregate(func, frame_rows, window.value_column)
