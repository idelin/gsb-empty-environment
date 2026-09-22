"""Local in-memory tables.

Tables are synthetic relation data supplied in-process; there is no SQL
engine and no external database anywhere in the package.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .errors import UnknownTableError
from .types import Relation, Schema


@dataclass
class Table:
    schema: Schema
    rows: list[tuple] = field(default_factory=list)

    @classmethod
    def create(cls, schema: Schema, rows) -> "Table":
        table = cls(schema=schema)
        for row in rows:
            table.add_row(row)
        return table

    def add_row(self, row) -> None:
        relation = Relation(self.schema)
        relation.add_row(row)
        self.rows.append(relation.rows[-1])

    def relation(self) -> Relation:
        return Relation(self.schema, [tuple(row) for row in self.rows])


class Catalog:
    """A read-only mapping of table name -> :class:`Table`."""

    def __init__(self, tables: dict | None = None):
        self._tables: dict[str, Table] = dict(tables or {})

    def add(self, name: str, table: Table) -> "Catalog":
        if not isinstance(name, str) or not name:
            raise ValueError("table name must be a non-empty string")
        self._tables[name] = table
        return self

    def get(self, name: str) -> Relation:
        table = self._tables.get(name)
        if table is None:
            raise UnknownTableError(f"unknown table {name!r}")
        return table.relation()

    def __contains__(self, name: str) -> bool:
        return name in self._tables
