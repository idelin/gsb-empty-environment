"""Synthetic local relation tables with explicit NULL / multiset semantics.

* Tables are *bags* (multisets): duplicate rows are stored and preserved.
  Joins and unions therefore multiply multiplicities; there is no
  implicit DISTINCT.
* NULL is a dedicated sentinel, never Python's ``None``. It follows
  three-valued logic (TRUE / FALSE / UNKNOWN); UNKNOWN is represented
  by the NULL sentinel.
"""

from __future__ import annotations

from dataclasses import dataclass


class _Null:
    _instance = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        # Force callers to be explicit; accidental boolean use is a bug.
        raise TypeError("NULL has no truth value: use three-valued logic")

    def __reduce__(self) -> tuple:
        return (_Null, ())


NULL = _Null()


def is_null(value: object) -> bool:
    return value is NULL


# -- three-valued logic ----------------------------------------------------
def sql_and(a: object, b: object) -> object:
    if a is False or b is False:
        return False
    if a is NULL or b is NULL:
        return NULL
    return True


def sql_or(a: object, b: object) -> object:
    if a is True or b is True:
        return True
    if a is NULL or b is NULL:
        return NULL
    return False


def sql_not(a: object) -> object:
    if a is NULL:
        return NULL
    return not a


def sql_eq(a: object, b: object) -> object:
    """Equality with SQL semantics: NULL = anything is UNKNOWN."""
    if is_null(a) or is_null(b):
        return NULL
    return a == b


def sql_lt(a: object, b: object) -> object:
    if is_null(a) or is_null(b):
        return NULL
    return a < b


Row = tuple


@dataclass
class Table:
    """A named bag of tuples with a fixed column order."""

    name: str
    columns: tuple[str, ...]
    rows: list[tuple]

    def col_index(self, name: str) -> int:
        return self.columns.index(name)

    def cardinality(self) -> int:
        return len(self.rows)
