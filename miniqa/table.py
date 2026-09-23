from __future__ import annotations

from bisect import bisect_left, bisect_right
from dataclasses import dataclass
from typing import Any, Iterator


Row = tuple[Any, ...]


@dataclass(frozen=True)
class ColumnStats:
    distinct: int
    minimum: Any
    maximum: Any
    null_count: int
    values: frozenset[Any]


@dataclass(frozen=True)
class Index:
    column: str
    entries: tuple[tuple[Any, tuple[int, int]], ...]

    def __post_init__(self) -> None:
        object.__setattr__(self, "entries", tuple(sorted(self.entries, key=lambda item: (item[0], item[1]))))

    def seek(self, operator: str, value: Any) -> Iterator[tuple[int, int]]:
        if value is None:
            return
        keys = [entry[0] for entry in self.entries]
        if operator == "=":
            start = bisect_left(keys, value)
            end = bisect_right(keys, value)
        elif operator == "<":
            start = 0
            end = bisect_left(keys, value)
        elif operator == "<=":
            start = 0
            end = bisect_right(keys, value)
        elif operator == ">":
            start = bisect_right(keys, value)
            end = len(self.entries)
        elif operator == ">=":
            start = bisect_left(keys, value)
            end = len(self.entries)
        else:
            return
        for index in range(start, end):
            yield self.entries[index][1]


@dataclass(frozen=True)
class Table:
    name: str
    columns: tuple[str, ...]
    rows: tuple[Row, ...]
    multiplicity: tuple[int, ...]
    stats: dict[str, ColumnStats]
    indexes: dict[str, Index]
    version: int = 0

    def position(self, column: str) -> int:
        return self.columns.index(column)


def build_stats(rows: tuple[Row, ...], columns: tuple[str, ...]) -> dict[str, ColumnStats]:
    result: dict[str, ColumnStats] = {}
    for position, column in enumerate(columns):
        values = [row[position] for row in rows if row[position] is not None]
        result[column] = ColumnStats(
            distinct=len(set(values)),
            minimum=min(values) if values else None,
            maximum=max(values) if values else None,
            null_count=len(rows) - len(values),
            values=frozenset(values),
        )
    return result


def build_indexes(
    rows: tuple[Row, ...],
    multiplicity: tuple[int, ...],
    columns: tuple[str, ...],
    indexed_columns: tuple[str, ...],
) -> dict[str, Index]:
    result: dict[str, Index] = {}
    for column in indexed_columns:
        position = columns.index(column)
        entries = [
            (row[position], (row_id, multiplicity[row_id]))
            for row_id, row in enumerate(rows)
            if row[position] is not None
        ]
        result[column] = Index(column=column, entries=tuple(entries))
    return result


def make_table(
    name: str,
    columns: tuple[str, ...],
    rows: tuple[Row, ...] | list[Row],
    indexed_columns: tuple[str, ...] = (),
    multiplicity: tuple[int, ...] | None = None,
    version: int = 0,
) -> Table:
    row_tuple = tuple(rows)
    if multiplicity is None:
        multiplicity_tuple = tuple(1 for _ in row_tuple)
    else:
        multiplicity_tuple = tuple(multiplicity)
    if len(multiplicity_tuple) != len(row_tuple):
        raise ValueError("multiplicity must have one value per row")
    return Table(
        name=name,
        columns=columns,
        rows=row_tuple,
        multiplicity=multiplicity_tuple,
        stats=build_stats(row_tuple, columns),
        indexes=build_indexes(row_tuple, multiplicity_tuple, columns, indexed_columns),
        version=version,
    )


class Catalog:
    def __init__(self, tables: dict[str, Table] | None = None) -> None:
        self._tables = dict(tables or {})
        self._stats_version = 0

    @property
    def stats_version(self) -> int:
        return self._stats_version

    def get(self, name: str) -> Table:
        return self._tables[name]

    def replace_table(self, table: Table) -> None:
        self._tables[table.name] = Table(
            name=table.name,
            columns=table.columns,
            rows=table.rows,
            multiplicity=table.multiplicity,
            stats=table.stats,
            indexes=table.indexes,
            version=table.version + 1,
        )
        self._stats_version += 1

    def refresh_stats(self, name: str, indexed_columns: tuple[str, ...] | None = None) -> None:
        table = self.get(name)
        index_columns = indexed_columns if indexed_columns is not None else tuple(table.indexes)
        refreshed = Table(
            name=table.name,
            columns=table.columns,
            rows=table.rows,
            multiplicity=table.multiplicity,
            stats=build_stats(table.rows, table.columns),
            indexes=build_indexes(table.rows, table.multiplicity, table.columns, index_columns),
            version=table.version + 1,
        )
        self._tables[name] = refreshed
        self._stats_version += 1
