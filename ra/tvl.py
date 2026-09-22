"""NULL sentinel and SQL three-valued logic (TRUE / FALSE / UNKNOWN)."""

from __future__ import annotations

from enum import Enum


class _Null:
    """Singleton NULL marker. Distinct from any regular value, including None."""

    _instance: "_Null | None" = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        raise TypeError("NULL has no truth value; use three-valued logic")


NULL = _Null()


class Tri(Enum):
    """Three-valued truth. Ordering FALSE < UNKNOWN < TRUE gives AND=min, OR=max."""

    FALSE = 0
    UNKNOWN = 1
    TRUE = 2

    def __and__(self, other: "Tri") -> "Tri":
        return Tri(min(self.value, other.value))

    def __or__(self, other: "Tri") -> "Tri":
        return Tri(max(self.value, other.value))

    def __invert__(self) -> "Tri":
        return _NOT[self]

    def is_true(self) -> bool:
        return self is Tri.TRUE


_NOT = {Tri.TRUE: Tri.FALSE, Tri.FALSE: Tri.TRUE, Tri.UNKNOWN: Tri.UNKNOWN}
