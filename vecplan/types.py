"""Logical types for the vector execution engine."""
from __future__ import annotations


class Type:
    """A logical column type."""

    def __init__(self, name, kind, min_value=None, max_value=None):
        self.name = name
        self.kind = kind  # 'bool' | 'int' | 'float' | 'string'
        self.min_value = min_value
        self.max_value = max_value

    @property
    def is_numeric(self):
        return self.kind in ("int", "float")

    @property
    def is_integer(self):
        return self.kind == "int"

    def __repr__(self):
        return self.name

    def __eq__(self, other):
        return isinstance(other, Type) and self.name == other.name

    def __hash__(self):
        return hash(self.name)


BOOL = Type("BOOL", "bool")
INT32 = Type("INT32", "int", -(2 ** 31), 2 ** 31 - 1)
INT64 = Type("INT64", "int", -(2 ** 63), 2 ** 63 - 1)
FLOAT64 = Type("FLOAT64", "float")
STRING = Type("STRING", "string")

ALL_TYPES = {t.name: t for t in (BOOL, INT32, INT64, FLOAT64, STRING)}


def promote_numeric(left, right):
    """Result type of an arithmetic operation on two numeric types."""
    if left.kind == "float" or right.kind == "float":
        return FLOAT64
    if left == INT64 or right == INT64:
        return INT64
    return INT32


def common_type(left, right):
    """Least common type for the two branches of an IF, or None."""
    if left == right:
        return left
    if left.is_numeric and right.is_numeric:
        return promote_numeric(left, right)
    return None
