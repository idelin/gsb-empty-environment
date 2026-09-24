"""Key encoding for composite indexes.

Every indexed column value is encoded into a comparable *component* so that a
full index key is a plain tuple of components and plain tuple comparison gives
exactly the index's physical ordering (per-column ASC/DESC + NULL placement).

Component layout: (tag, payload)
  tag 0: -infinity sentinel (below NULL and every value)
  tag 1: NULL when nulls_first
  tag 2: a real value (payload wrapped in Rev for DESC columns)
  tag 3: NULL when nulls_last
  tag 4: +infinity sentinel
"""
from __future__ import annotations

from dataclasses import dataclass
from functools import total_ordering

T_LO = 0
T_NULL_LO = 1
T_VAL = 2
T_NULL_HI = 3
T_HI = 4


@total_ordering
class Rev:
    """Order-reversing wrapper: Rev(a) < Rev(b)  iff  b < a."""

    __slots__ = ("v",)

    def __init__(self, v):
        self.v = v

    def __eq__(self, other):
        return isinstance(other, Rev) and self.v == other.v

    def __lt__(self, other):
        if not isinstance(other, Rev):
            return NotImplemented
        return other.v < self.v

    def __hash__(self):
        return hash(("Rev", self.v))

    def __repr__(self):
        return f"Rev({self.v!r})"


@dataclass(frozen=True)
class ColSpec:
    col: int
    desc: bool = False
    nulls_first: bool = True


LO_COMP = (T_LO, None)
HI_COMP = (T_HI, None)


def encode_value(value, spec: ColSpec):
    """Encode one column value into a comparable component."""
    if value is None:
        return (T_NULL_LO if spec.nulls_first else T_NULL_HI, None)
    return (T_VAL, Rev(value) if spec.desc else value)


def encode_key(raw_values, spec):
    """Encode a tuple of raw indexed-column values into a full key."""
    return tuple(encode_value(v, s) for v, s in zip(raw_values, spec))


def low_key(n):
    return (LO_COMP,) * n


def high_key(n):
    return (HI_COMP,) * n
