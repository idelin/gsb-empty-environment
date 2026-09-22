from __future__ import annotations

from .relations import Table

_ACTIVE_TABLES: dict[str, Table] = {}


def set_active_tables(tables: dict[str, Table]) -> None:
    _ACTIVE_TABLES.clear()
    _ACTIVE_TABLES.update(tables)


def get_active_table(name: str) -> Table:
    try:
        return _ACTIVE_TABLES[name]
    except KeyError as exc:
        raise KeyError(f"unknown table {name}; call sample_tables() before executing plans") from exc
