"""Three-valued logic (SQL-style): TRUE, FALSE, UNKNOWN.

UNKNOWN propagates: NOT UNKNOWN is UNKNOWN, not TRUE.
"""

from __future__ import annotations

import enum


class TVL(enum.Enum):
    FALSE = 0
    UNKNOWN = 1
    TRUE = 2

    def __invert__(self) -> "TVL":
        # NOT: UNKNOWN stays UNKNOWN.
        if self is TVL.TRUE:
            return TVL.FALSE
        if self is TVL.FALSE:
            return TVL.TRUE
        return TVL.UNKNOWN

    def __and__(self, other: "TVL") -> "TVL":
        # FALSE dominates; UNKNOWN dominates TRUE.
        if self is TVL.FALSE or other is TVL.FALSE:
            return TVL.FALSE
        if self is TVL.UNKNOWN or other is TVL.UNKNOWN:
            return TVL.UNKNOWN
        return TVL.TRUE

    def __or__(self, other: "TVL") -> "TVL":
        # TRUE dominates; UNKNOWN dominates FALSE.
        if self is TVL.TRUE or other is TVL.TRUE:
            return TVL.TRUE
        if self is TVL.UNKNOWN or other is TVL.UNKNOWN:
            return TVL.UNKNOWN
        return TVL.FALSE

    @staticmethod
    def of(value: bool) -> "TVL":
        return TVL.TRUE if value else TVL.FALSE
