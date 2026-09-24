"""Key-ordering primitives for composite indexes.

A composite key is a tuple with one element per index column.  Each column
has a sort direction (ASC/DESC) and a NULL placement (NULLS FIRST/LAST).
All ordering decisions are made by ``cmp_value``/``compare_keys`` so the
rest of the system never re-implements direction or NULL logic.
"""
from __future__ import annotations

import functools
from dataclasses import dataclass


class _Sentinel:
    __slots__ = ("name",)

    def __init__(self, name):
        self.name = name

    def __repr__(self):
        return self.name


NULL = _Sentinel("NULL")   # SQL NULL value stored in rows/keys
MIN = _Sentinel("-INF")    # sorts below every real key (encoding artifact)
MAX = _Sentinel("+INF")    # sorts above every real key (encoding artifact)


@dataclass(frozen=True)
class ColumnSpec:
    name: str
    desc: bool = False
    nulls_first: bool = None  # default: FIRST for DESC, LAST for ASC

    def __post_init__(self):
        if self.nulls_first is None:
            object.__setattr__(self, "nulls_first", self.desc)


def _rank(spec: ColumnSpec, v) -> int:
    if v is MIN:
        return -1
    if v is MAX:
        return 3
    if v is NULL:
        return 0 if spec.nulls_first else 2
    return 1


def cmp_value(spec: ColumnSpec, x, y) -> int:
    """Three-way compare of two values under one column's ordering."""
    rx, ry = _rank(spec, x), _rank(spec, y)
    if rx != ry:
        return -1 if rx < ry else 1
    if rx == 1:
        c = (x > y) - (x < y)
        return -c if spec.desc else c
    return 0


def compare_keys(specs, k1, k2) -> int:
    """Lexicographic compare of two (possibly partial) key tuples."""
    for spec, x, y in zip(specs, k1, k2):
        c = cmp_value(spec, x, y)
        if c:
            return c
    return (len(k1) > len(k2)) - (len(k1) < len(k2))


def key_sorter(specs):
    return functools.cmp_to_key(lambda a, b: compare_keys(specs, a, b))
