from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass
from functools import total_ordering
from heapq import merge
import itertools
from typing import Any

from .expressions import Expression, COMPARATORS
from .catalog import get_active_table
from .relations import Column, Provenance, Relation, Row, Schema


class Node(ABC):
    @property
    @abstractmethod
    def schema(self) -> Schema: ...

    @abstractmethod
    def execute(self, tables: dict[str, Any]) -> Relation: ...


@dataclass(frozen=True)
class Scan(Node):
    table_name: str

    @property
    def schema(self) -> Schema:
        return get_active_table(self.table_name).schema

    def execute(self, tables: dict[str, Any]) -> Relation:
        table = tables.get(self.table_name, get_active_table(self.table_name))
        return Relation(table.schema, table.rows)


@dataclass(frozen=True)
class Filter(Node):
    source: Node
    predicate: Expression

    @property
    def schema(self) -> Schema:
        return self.source.schema

    def execute(self, tables: dict[str, Any]) -> Relation:
        return Relation(self.schema, tuple(row for row in self.source.execute(tables).rows if self.predicate.evaluate(row) is True))


@dataclass(frozen=True)
class Join(Node):
    left: Node
    right: Node
    join_type: str
    left_keys: tuple[Column, ...]
    right_keys: tuple[Column, ...]
    predicate: Expression | None

    @property
    def schema(self) -> Schema:
        return Schema(self.left.schema.columns + self.right.schema.columns)

    def execute(self, tables: dict[str, Any]) -> Relation:
        left_relation = self.left.execute(tables)
        right_relation = self.right.execute(tables)
        return execute_join(
            left_relation,
            right_relation,
            self.join_type,
            self.left_keys,
            self.right_keys,
            self.schema,
            self.predicate,
        )


def key_value(row: Row, keys: tuple[Column, ...]) -> tuple[Any, ...] | None:
    values = tuple(row[key] for key in keys)
    if any(value is None for value in values):
        return None
    return values


def execute_join(
    left_relation: Relation,
    right_relation: Relation,
    join_type: str,
    left_keys: tuple[Column, ...],
    right_keys: tuple[Column, ...],
    output_schema: Schema,
    predicate: Expression | None,
) -> Relation:
    right_by_key: dict[tuple[Any, ...], list[Row]] = {}
    for row in right_relation.rows:
        key = key_value(row, right_keys)
        if key is not None:
            right_by_key.setdefault(key, []).append(row)

    output: list[Row] = []
    matched_right_ids: set[int] = set()

    for left_row in left_relation.rows:
        matched = False
        left_key = key_value(row=left_row, keys=left_keys)
        candidates = right_by_key.get(left_key, []) if left_key is not None else []
        for right_row in candidates:
            candidate = Row(output_schema, left_row.values + right_row.values, left_row.provenance)
            if predicate is None or predicate.evaluate(candidate) is True:
                output.append(candidate)
                matched = True
                matched_right_ids.add(id(right_row))
        if not matched and join_type in {"left", "full"}:
            values = left_row.values + (None,) * len(right_relation.schema.columns)
            output.append(Row(output_schema, values, left_row.provenance))

    if join_type in {"right", "full"}:
        for right_row in right_relation.rows:
            if id(right_row) not in matched_right_ids:
                values = (None,) * len(left_relation.schema.columns) + right_row.values
                output.append(Row(output_schema, values, right_row.provenance))

    return Relation(output_schema, tuple(output))


@dataclass(frozen=True)
class OrderTerm:
    column: Column
    descending: bool = False
    nulls_last: bool | None = None

    def nulls_are_last(self) -> bool:
        return self.nulls_last if self.nulls_last is not None else self.descending


@total_ordering
@dataclass(frozen=True)
class SortKeyPart:
    is_null: bool
    null_rank: int
    value: Any
    descending: bool

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, SortKeyPart):
            return NotImplemented
        return (self.is_null, self.value) == (other.is_null, other.value)

    def __lt__(self, other: "SortKeyPart") -> bool:
        if self.is_null or other.is_null:
            if self.is_null != other.is_null:
                return self.null_rank < other.null_rank
            return False
        if self.value == other.value:
            return False
        base = self.value < other.value
        return not base if self.descending else base


def sort_key(row: Row, order: tuple[OrderTerm, ...]) -> tuple[SortKeyPart, ...]:
    parts = []
    for term in order:
        value = row[term.column]
        is_null = value is None
        null_rank = 1 if term.nulls_are_last() else 0
        parts.append(SortKeyPart(is_null, null_rank, value, term.descending))
    return tuple(parts)


@dataclass(frozen=True)
class Sort(Node):
    source: Node
    order: tuple[OrderTerm, ...]

    @property
    def schema(self) -> Schema:
        return self.source.schema

    def execute(self, tables: dict[str, Any]) -> Relation:
        rows = self.source.execute(tables).rows
        return Relation(self.schema, tuple(sorted(rows, key=lambda row: (sort_key(row, self.order), row.provenance))))


@dataclass(frozen=True)
class Limit(Node):
    source: Node
    offset: int
    limit: int | None

    @property
    def schema(self) -> Schema:
        return self.source.schema

    def execute(self, tables: dict[str, Any]) -> Relation:
        rows = self.source.execute(tables).rows
        end = None if self.limit is None else self.offset + self.limit
        return Relation(self.schema, rows[self.offset : end])


@dataclass(frozen=True)
class LocalTopK(Node):
    partition_id: int
    source: Node
    order: tuple[OrderTerm, ...]
    offset: int
    limit: int | None

    @property
    def schema(self) -> Schema:
        return self.source.schema

    def execute(self, tables: dict[str, Any]) -> Relation:
        rows = self.source.execute(tables).rows
        ordered = sorted(rows, key=lambda row: (sort_key(row, self.order), row.provenance))
        end = None if self.limit is None else self.offset + self.limit
        return Relation(self.schema, tuple(ordered[:end]))


@dataclass(frozen=True)
class MergeTopK(Node):
    children: tuple[Node, ...]
    order: tuple[OrderTerm, ...]
    offset: int
    limit: int | None

    @property
    def schema(self) -> Schema:
        return self.children[0].schema

    def execute(self, tables: dict[str, Any]) -> Relation:
        relations = [child.execute(tables).rows for child in self.children]
        streams = [
            iter(sorted(rows, key=lambda row: (sort_key(row, self.order), row.provenance)))
            for rows in relations
        ]
        merged = merge(
            *streams,
            key=lambda row: (sort_key(row, self.order), row.provenance),
        )
        end = None if self.limit is None else self.offset + self.limit
        return Relation(self.schema, tuple(itertools.islice(merged, self.offset, end)))
