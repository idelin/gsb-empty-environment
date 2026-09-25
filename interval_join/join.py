"""Join points or half-open intervals against half-open intervals."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable

from interval_join.batches import (
    FLOAT,
    INT,
    TypedBatch,
    is_nan,
    is_null,
    normalize_batches,
)
from interval_join.index import IndexedInterval, IntervalIndex


@dataclass(frozen=True, slots=True)
class PointMatch:
    """A point and interval that match, with zero-length overlap."""

    point_id: object
    interval_id: object
    overlap_length: int


@dataclass(frozen=True, slots=True)
class IntervalMatch:
    """Two overlapping intervals and the length of their intersection."""

    left_id: object
    right_id: object
    overlap_length: int | float


def _require_same_schema(batches: Iterable[TypedBatch], table_name: str) -> None:
    first: dict[str, str] | None = None
    for batch_number, batch in enumerate(batches):
        columns = dict(batch.columns)
        if first is None:
            first = columns
        elif columns != first:
            raise TypeError(f"{table_name} batch {batch_number} has an incompatible schema")


def _require_endpoint_column(
    batches: Iterable[TypedBatch],
    column: str,
    table_name: str,
) -> None:
    for batch_number, batch in enumerate(batches):
        if column not in batch.columns:
            raise ValueError(f"{table_name} batch {batch_number} is missing {column!r}")
        if batch.columns[column] not in (INT, FLOAT):
            raise TypeError(
                f"{table_name} column {column!r} must be INT or FLOAT, "
                f"got {batch.columns[column]}"
            )


def _present_number(value: object, column: str) -> int | float | None:
    if is_null(value) or is_nan(value):
        return None
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise TypeError(f"column {column!r} contains a non-numeric value")
    return value


def _valid_intervals(
    batches: Iterable[TypedBatch],
    id_col: str,
    start_col: str,
    end_col: str,
    table_name: str,
) -> list[IndexedInterval]:
    intervals: list[IndexedInterval] = []
    for batch_number, batch in enumerate(batches):
        if id_col not in batch.columns:
            raise ValueError(f"{table_name} batch {batch_number} is missing {id_col!r}")
        for row in batch.rows:
            start = _present_number(row.get(start_col), start_col)
            end = _present_number(row.get(end_col), end_col)
            if start is None or end is None or start >= end:
                continue
            intervals.append(IndexedInterval(start, end, row.get(id_col)))
    return intervals


def _prepare_interval_batches(
    batches: TypedBatch | Iterable[TypedBatch],
    id_col: str,
    start_col: str,
    end_col: str,
    table_name: str,
) -> list[IndexedInterval]:
    normalized = normalize_batches(batches)
    _require_same_schema(normalized, table_name)
    _require_endpoint_column(normalized, start_col, table_name)
    _require_endpoint_column(normalized, end_col, table_name)
    return _valid_intervals(normalized, id_col, start_col, end_col, table_name)


def point_join(
    point_batches: TypedBatch | Iterable[TypedBatch],
    interval_batches: TypedBatch | Iterable[TypedBatch],
    *,
    point_id_col: str = "id",
    point_col: str = "point",
    interval_id_col: str = "id",
    start_col: str = "start",
    end_col: str = "end",
) -> list[PointMatch]:
    """Join points to indexed half-open intervals.

    Matching follows start <= point < end. NULL and NaN points never match.
    A point source ID of NULL or NaN is retained unchanged.
    """

    points = normalize_batches(point_batches)
    intervals = normalize_batches(interval_batches)
    _require_same_schema(points, "point")
    _require_same_schema(intervals, "interval")
    _require_endpoint_column(points, point_col, "point")

    indexed_intervals = _prepare_interval_batches(
        intervals, interval_id_col, start_col, end_col, "interval"
    )
    index = IntervalIndex(indexed_intervals)
    matches: list[PointMatch] = []
    for batch in points:
        if point_id_col not in batch.columns:
            raise ValueError(f"point batch is missing {point_id_col!r}")
        for row in batch.rows:
            point = _present_number(row.get(point_col), point_col)
            if point is None:
                continue
            point_id = row.get(point_id_col)
            for interval in index.query_point(point):
                matches.append(PointMatch(point_id, interval.source_id, 0))
    return matches


def interval_join(
    left_batches: TypedBatch | Iterable[TypedBatch],
    right_batches: TypedBatch | Iterable[TypedBatch],
    *,
    left_id_col: str = "id",
    left_start_col: str = "start",
    left_end_col: str = "end",
    right_id_col: str = "id",
    right_start_col: str = "start",
    right_end_col: str = "end",
) -> list[IntervalMatch]:
    """Join two sets of half-open intervals.

    The left side is indexed and each right interval is a range query. Touching
    endpoints do not overlap because both ends are exclusive. Integer endpoint
    arithmetic remains integer arithmetic.
    """

    left = _prepare_interval_batches(
        left_batches, left_id_col, left_start_col, left_end_col, "left"
    )
    right = _prepare_interval_batches(
        right_batches, right_id_col, right_start_col, right_end_col, "right"
    )
    index = IntervalIndex(left)
    matches: list[IntervalMatch] = []
    for candidate in right:
        for indexed in index.query_range(candidate.start, candidate.end):
            overlap_start = max(indexed.start, candidate.start)
            overlap_end = min(indexed.end, candidate.end)
            matches.append(
                IntervalMatch(
                    indexed.source_id,
                    candidate.source_id,
                    overlap_end - overlap_start,
                )
            )
    return matches
