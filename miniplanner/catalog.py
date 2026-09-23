"""Local in-memory relations and simple column statistics."""

from __future__ import annotations

from bisect import bisect_left, bisect_right
from dataclasses import dataclass
from typing import Any, Iterable, Mapping, Sequence

from miniplanner.expr import ComparisonOp


def logical_type(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "bool"
    return type(value).__name__


def comparable_types(left_type: str | None, right_type: str) -> bool:
    if left_type is None or left_type == right_type:
        return True
    return left_type in {"int", "float"} and right_type in {"int", "float"}


def type_family(value_type: str | None) -> str | None:
    if value_type in {"int", "float"}:
        return "number"
    return value_type


def sql_compare(left: Any, right: Any, op: ComparisonOp) -> bool | None:
    """Compare values using SQL-style three-valued logic.

    A missing operand makes every comparison unknown. Non-null values from
    unrelated type families are not implicitly converted: equality is false,
    inequality is true, and ordered comparisons are unknown.
    """
    if left is None or right is None:
        return None
    left_type = logical_type(left)
    right_type = logical_type(right)
    same_family = left_type == right_type or (
        left_type in {"int", "float"} and right_type in {"int", "float"}
    )
    if op == "=":
        return False if not same_family else left == right
    if op == "<>":
        return True if not same_family else left != right
    if not same_family:
        return None
    if op == "<":
        return left < right
    if op == "<=":
        return left <= right
    if op == ">":
        return left > right
    return left >= right


@dataclass(frozen=True)
class ColumnStats:
    min_value: Any = None
    max_value: Any = None
    null_count: int = 0
    distinct_nonnull: int = 0
    value_type: str | None = None

    @classmethod
    def from_rows(cls, rows: Sequence[Mapping[str, Any]], column: str) -> "ColumnStats":
        values = [row.get(column) for row in rows]
        nonnull = [value for value in values if value is not None]
        if not nonnull:
            return cls(null_count=len(values))
        sample_type = logical_type(nonnull[0])
        same_family = [
            value for value in nonnull if comparable_types(sample_type, logical_type(value))
        ]
        if len(same_family) != len(nonnull):
            return cls(
                null_count=sum(value is None for value in values),
                distinct_nonnull=len(nonnull),
                value_type="mixed",
            )
        ordered = same_family
        distinct = len(set(nonnull))
        return cls(
            min_value=min(ordered),
            max_value=max(ordered),
            null_count=sum(value is None for value in values),
            distinct_nonnull=distinct,
            value_type=sample_type,
        )


class Table:
    """A multiset of dictionary rows with sorted local indexes."""

    def __init__(
        self,
        name: str,
        rows: Iterable[Mapping[str, Any]],
        stats: Mapping[str, ColumnStats] | None = None,
    ) -> None:
        self.name = name
        self.rows = [dict(row) for row in rows]
        self.stats = dict(stats or {})
        self._indexes: dict[str, dict[str, list[tuple[Any, int]]]] = {}

    @property
    def row_count(self) -> int:
        return len(self.rows)

    def column_stats(self, column: str) -> ColumnStats:
        if column in self.stats:
            return self.stats[column]
        return ColumnStats.from_rows(self.rows, column)

    def with_stats(self, stats: Mapping[str, ColumnStats]) -> "Table":
        return Table(self.name, self.rows, stats)

    def index(self, column: str) -> dict[str, list[tuple[Any, int]]]:
        if column not in self._indexes:
            groups: dict[str, list[tuple[Any, int]]] = {}
            for row_id, row in enumerate(self.rows):
                value = row.get(column)
                if value is not None:
                    family = type_family(logical_type(value))
                    groups.setdefault(family, []).append((value, row_id))
            for entries in groups.values():
                entries.sort(key=lambda item: (item[0], item[1]))
            self._indexes[column] = groups
        return self._indexes[column]

    def index_lookup(self, column: str, op: ComparisonOp, value: Any) -> list[int]:
        if value is None:
            return []
        stats = self.column_stats(column)
        value_type = logical_type(value)
        available = self.index(column)
        if stats.value_type == "mixed":
            entries = available.get(type_family(value_type), [])
        elif not comparable_types(stats.value_type, value_type):
            return []
        else:
            entries = available.get(type_family(value_type), [])
        keys = [entry[0] for entry in entries]
        if op == "=":
            start = bisect_left(keys, value)
            stop = bisect_right(keys, value)
        elif op == "<":
            start, stop = 0, bisect_left(keys, value)
        elif op == "<=":
            start, stop = 0, bisect_right(keys, value)
        elif op == ">":
            start, stop = bisect_right(keys, value), len(entries)
        elif op == ">=":
            start, stop = bisect_left(keys, value), len(entries)
        else:
            return []
        return [entries[pos][1] for pos in range(start, stop)]

    def value_category(self, column: str, value: Any) -> str:
        """Return the type-sensitive statistical range category of a value."""
        if value is None:
            return "null"
        stats = self.column_stats(column)
        value_type = logical_type(value)
        if stats.value_type == "mixed":
            available_families = set(self.index(column))
            if type_family(value_type) in available_families:
                return "within_mid"
            return "incompatible"
        if not comparable_types(stats.value_type, value_type):
            return "incompatible"
        nonnull_count = self.row_count - stats.null_count
        if nonnull_count <= 0 or stats.min_value is None or stats.max_value is None:
            return "empty_domain"
        if value < stats.min_value:
            return "below_min"
        if value > stats.max_value:
            return "above_max"
        if stats.min_value == stats.max_value:
            return "within_low"
        if value_type not in {"int", "float"}:
            return "within_mid"
        span = stats.max_value - stats.min_value
        position = (value - stats.min_value) / span
        if position < 0.34:
            return "within_low"
        if position >= 0.67:
            return "within_high"
        return "within_mid"


class Catalog:
    def __init__(self, tables: Iterable[Table] = ()) -> None:
        self.tables = {table.name: table for table in tables}
        self.version = 1

    def get(self, name: str) -> Table:
        try:
            return self.tables[name]
        except KeyError as exc:
            raise KeyError(f"unknown table: {name}") from exc

    def replace_table(self, table: Table) -> None:
        self.tables[table.name] = table
        self.version += 1
