"""Column identity and schema shape.

A column has a stable identity (`cid`) that survives renames; the name is
just a label.  Schemas reject duplicate identities and duplicate names.
"""
from __future__ import annotations

from dataclasses import dataclass

from .typesys import Type


class SchemaError(Exception):
    pass


@dataclass(frozen=True)
class Column:
    cid: str
    name: str
    type: Type
    nullable: bool = True


class Schema:
    __slots__ = ("_columns", "_by_cid")

    def __init__(self, columns):
        cols = tuple(columns)
        seen_ids, seen_names = set(), set()
        for col in cols:
            if not isinstance(col, Column):
                raise SchemaError(f"not a Column: {col!r}")
            if col.cid in seen_ids:
                raise SchemaError(f"duplicate column identity {col.cid!r}")
            if col.name in seen_names:
                raise SchemaError(f"duplicate column name {col.name!r}")
            seen_ids.add(col.cid)
            seen_names.add(col.name)
        self._columns = cols
        self._by_cid = {c.cid: c for c in cols}

    @property
    def columns(self):
        return self._columns

    def __len__(self):
        return len(self._columns)

    def __iter__(self):
        return iter(self._columns)

    def has(self, cid: str) -> bool:
        return cid in self._by_cid

    def column(self, cid: str) -> Column:
        try:
            return self._by_cid[cid]
        except KeyError:
            raise SchemaError(f"unknown column identity {cid!r}") from None

    def names(self):
        return [c.name for c in self._columns]

    def __eq__(self, other):
        return isinstance(other, Schema) and self._columns == other._columns

    def __repr__(self):
        inner = ", ".join(
            f"{c.name}:{c.type.value}#{c.cid}" for c in self._columns)
        return f"Schema({inner})"
