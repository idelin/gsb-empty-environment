"""Local in-memory relation catalog.

Relations are *multisets* (bags): rows are plain lists and duplicates are kept.
No SQL engine is involved; a :class:`Catalog` is the only data source a Scan
can read.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from plan_dag.datatypes import BOOLEAN, DOUBLE, INTEGER, NULL_TYPE, TEXT, DataType, Schema, literal_type
from plan_dag.errors import BindingError, InvalidDocumentError, UnknownTableError


def _value_matches(value: Any, t: DataType) -> None:
    vt = literal_type(value)
    if vt == NULL_TYPE:
        return
    if vt != t:
        raise InvalidDocumentError(f"value {value!r} has type {vt}, schema declares {t}")


@dataclass(frozen=True)
class Table:
    name: str
    schema: Schema
    rows: tuple[tuple[Any, ...], ...]

    def __post_init__(self):
        for i, row in enumerate(self.rows):
            if not isinstance(row, (list, tuple)):
                raise InvalidDocumentError(f"table {self.name!r} row {i} must be a list")
            if len(row) != len(self.schema):
                raise InvalidDocumentError(
                    f"table {self.name!r} row {i} has {len(row)} values, "
                    f"schema has {len(self.schema)} fields"
                )
            for f, v in zip(self.schema, row):
                _value_matches(v, f.type)
        object.__setattr__(self, "rows", tuple(tuple(r) for r in self.rows))

    @staticmethod
    def from_data(data: object) -> "Table":
        if not isinstance(data, dict):
            raise InvalidDocumentError("table must be an object")
        extra = set(data) - {"name", "schema", "rows"}
        if extra:
            raise InvalidDocumentError(f"table has unknown keys {sorted(extra)}")
        name = data.get("name")
        if not isinstance(name, str) or not name:
            raise InvalidDocumentError("table 'name' must be a non-empty string")
        schema = Schema.from_data(data.get("schema"))
        rows = data.get("rows")
        if not isinstance(rows, list):
            raise InvalidDocumentError("table 'rows' must be a list")
        return Table(name=name, schema=schema, rows=tuple(rows))  # type: ignore[arg-type]

    def to_data(self) -> dict:
        return {
            "name": self.name,
            "schema": self.schema.to_data(),
            "rows": [list(r) for r in self.rows],
        }


class Catalog:
    def __init__(self, tables: list[Table] | None = None):
        self._tables: dict[str, Table] = {}
        for t in tables or []:
            self.add(t)

    def add(self, table: Table) -> None:
        if table.name in self._tables:
            raise BindingError(f"table {table.name!r} already in catalog")
        self._tables[table.name] = table

    def get(self, name: str) -> Table:
        try:
            return self._tables[name]
        except KeyError:
            raise UnknownTableError(
                f"unknown table {name!r}; known: {sorted(self._tables)}"
            ) from None

    def names(self) -> list[str]:
        return sorted(self._tables)
