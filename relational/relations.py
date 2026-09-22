from __future__ import annotations

from dataclasses import dataclass
from functools import total_ordering
from typing import Any, Mapping


class NullType:
    _instance = None

    def __new__(cls) -> "NullType":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        raise TypeError("SQL NULL has no Python truth value")


NULL = NullType()


@dataclass(frozen=True)
class Column:
    table: str
    name: str

    @property
    def qualified(self) -> str:
        return f"{self.table}.{self.name}"


@total_ordering
@dataclass(frozen=True)
class Provenance:
    partition: int
    index: int

    def __lt__(self, other: "Provenance") -> bool:
        return (self.partition, self.index) < (other.partition, other.index)


@dataclass(frozen=True)
class Schema:
    columns: tuple[Column, ...]

    def index(self, column: Column) -> int:
        try:
            return self.columns.index(column)
        except ValueError as exc:
            raise KeyError(f"unknown column {column.qualified}") from exc


@dataclass(frozen=True)
class Row:
    schema: Schema
    values: tuple[Any, ...]
    provenance: Provenance

    def __post_init__(self) -> None:
        if len(self.values) != len(self.schema.columns):
            raise ValueError("row value count must match schema column count")

    def __getitem__(self, column: Column | int) -> Any:
        if isinstance(column, int):
            return self.values[column]
        return self.values[self.schema.index(column)]

    def as_dict(self) -> dict[str, Any]:
        return {column.qualified: value for column, value in zip(self.schema.columns, self.values)}


@dataclass(frozen=True)
class Relation:
    schema: Schema
    rows: tuple[Row, ...]

    def __post_init__(self) -> None:
        for row in self.rows:
            if row.schema != self.schema:
                raise ValueError("relation contains a row with a different schema")


@dataclass(frozen=True)
class Table:
    name: str
    schema: Schema
    partitions: tuple[tuple[Row, ...], ...]
    unique_columns: frozenset[Column] = frozenset()

    @property
    def rows(self) -> tuple[Row, ...]:
        return tuple(row for partition in self.partitions for row in partition)


def make_table(
    name: str,
    column_names: tuple[str, ...],
    partitions: tuple[tuple[Mapping[str, Any], ...], ...] | tuple[tuple[dict[str, Any], ...], ...],
    *,
    unique_columns: tuple[str, ...] = (),
) -> Table:
    columns = tuple(Column(name, column_name) for column_name in column_names)
    schema = Schema(columns)
    made_partitions = []
    for partition_id, raw_partition in enumerate(partitions):
        made_rows = []
        for row_id, raw_row in enumerate(raw_partition):
            if tuple(raw_row.keys()) != column_names:
                raise ValueError("each synthetic row must provide exactly the declared columns")
            made_rows.append(
                Row(
                    schema,
                    tuple(raw_row[column_name] for column_name in column_names),
                    Provenance(partition_id, row_id),
                )
            )
        made_partitions.append(tuple(made_rows))
    return Table(
        name,
        schema,
        tuple(made_partitions),
        frozenset(Column(name, item) for item in unique_columns),
    )


def row_signature(row: Row) -> tuple[Any, ...]:
    return row.values


def relation_multiset(relation: Relation) -> dict[tuple[Any, ...], int]:
    result: dict[tuple[Any, ...], int] = {}
    for row in relation.rows:
        signature = row_signature(row)
        result[signature] = result.get(signature, 0) + 1
    return result
