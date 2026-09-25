"""Versioned schemas with stable column identities.

A column identity (column_id) survives renames and type changes; a name is
just an attribute of the identity. Schemas are immutable value objects.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass

from .types import TypeTag


class SchemaError(Exception):
    pass


_id_counter = itertools.count(1)


def new_column_id() -> str:
    return f"col-{next(_id_counter)}"


@dataclass(frozen=True)
class ColumnDef:
    column_id: str
    name: str
    type: TypeTag
    nullable: bool = True


def make_column(name, type, nullable=True, column_id=None) -> ColumnDef:
    return ColumnDef(column_id=column_id or new_column_id(), name=name,
                     type=type, nullable=nullable)


class Schema:
    def __init__(self, columns, version=0):
        columns = tuple(columns)
        names = [c.name for c in columns]
        if len(set(names)) != len(names):
            dupes = sorted({n for n in names if names.count(n) > 1})
            raise SchemaError(f"duplicate column names: {dupes}")
        ids = [c.column_id for c in columns]
        if len(set(ids)) != len(ids):
            raise SchemaError("duplicate column identities")
        self._columns = columns
        self.version = version

    @property
    def columns(self):
        return self._columns

    def names(self):
        return tuple(c.name for c in self._columns)

    def column(self, name) -> ColumnDef:
        for c in self._columns:
            if c.name == name:
                return c
        raise KeyError(name)

    def get(self, column_id):
        for c in self._columns:
            if c.column_id == column_id:
                return c
        return None

    def __eq__(self, other):
        return isinstance(other, Schema) and self._columns == other._columns

    def __repr__(self):
        parts = []
        for c in self._columns:
            marker = "?" if c.nullable else ""
            parts.append(f"{c.name}:{c.type.value}{marker}#{c.column_id}")
        cols = ", ".join(parts)
        return f"Schema(v{self.version}, [{cols}])"
