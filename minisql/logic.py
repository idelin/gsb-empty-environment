"""三值逻辑（SQL 风格）：TRUE / FALSE / UNKNOWN。

WHERE 子句只保留求值为 TRUE 的行；UNKNOWN 与 FALSE 一样被过滤，
这正是 NULL 分区可以被安全跳过的理论基础。
"""

from __future__ import annotations

from enum import Enum


class Tri(Enum):
    FALSE = 0
    UNKNOWN = 1
    TRUE = 2

    def __invert__(self) -> "Tri":  # NOT
        return _NOT[self]

    def __and__(self, other: "Tri") -> "Tri":
        return Tri(min(self.value, other.value))

    def __or__(self, other: "Tri") -> "Tri":
        return Tri(max(self.value, other.value))


_NOT = {Tri.FALSE: Tri.TRUE, Tri.UNKNOWN: Tri.UNKNOWN, Tri.TRUE: Tri.FALSE}
