"""Bounded enumeration of synthetic input databases over a finite domain.

Tables are enumerated as multisets of rows (combinations with
replacement), ordered by increasing row count, so small inputs are
explored first. No SQL engine is used anywhere.
"""
from __future__ import annotations

from itertools import combinations_with_replacement, product


def enum_tables(arity, domain, max_rows):
    """All tables (as sorted tuples of rows) with up to max_rows rows."""
    rows = list(product(domain, repeat=arity))
    tables = [()]
    for n in range(1, max_rows + 1):
        tables.extend(combinations_with_replacement(rows, n))
    return tables


def enum_databases(catalog, domain, max_rows):
    """Yield databases {table: [row, ...]} over the catalog, roughly in
    order of increasing total size."""
    names = list(catalog)
    per_table = [enum_tables(len(catalog[n]), domain, max_rows) for n in names]
    for combo in product(*per_table):
        yield {name: [tuple(r) for r in tbl] for name, tbl in zip(names, combo)}


def database_size(db):
    return sum(len(rows) for rows in db.values())
