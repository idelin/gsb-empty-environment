"""Core data model for window definitions.

Semantics fixed up-front (important for reuse decisions):

* ``NULL`` ordering is an explicit part of the ORDER BY signature.  When not
  supplied it defaults to the SQL standard / PostgreSQL convention:
  ``NULLS LAST`` for ASC, ``NULLS FIRST`` for DESC.  Two keys that only differ
  in null placement are *not* directly compatible.
* PARTITION BY is a multiset of columns: column order is irrelevant
  (``PARTITION BY a, b`` equals ``PARTITION BY b, a``) but duplicates count.
* ORDER BY is a sequence: order and prefix length matter.
* Two ROWS frames over the same ordered stream may reuse the sort, but each
  frame owns its own result rows - sorted streams are never "shared results".
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

# Frame boundary kinds.
UNBOUNDED_PRECEDING = "UNBOUNDED PRECEDING"
PRECEDING = "PRECEDING"
CURRENT_ROW = "CURRENT ROW"
FOLLOWING = "FOLLOWING"
UNBOUNDED_FOLLOWING = "UNBOUNDED FOLLOWING"

BoundKind = Literal[
    "UNBOUNDED PRECEDING",
    "PRECEDING",
    "CURRENT ROW",
    "FOLLOWING",
    "UNBOUNDED FOLLOWING",
]


@dataclass(frozen=True)
class FrameBound:
    """One endpoint of a ROWS frame.

    ``offset`` is the positive integer used with PRECEDING/FOLLOWING and is
    ignored otherwise.
    """

    kind: BoundKind
    offset: int | None = None

    def __post_init__(self) -> None:
        if self.kind in ("PRECEDING", "FOLLOWING"):
            if self.offset is None:
                raise ValueError(f"{self.kind} requires an offset")
            if self.offset <= 0:
                raise ValueError("frame offset must be a positive integer")
        elif self.offset is not None:
            raise ValueError(f"offset not allowed for {self.kind}")

    def __str__(self) -> str:
        if self.kind in ("PRECEDING", "FOLLOWING"):
            row_word = "ROW" if self.offset == 1 else "ROWS"
            return f"{self.offset} {row_word} {self.kind}"
        return self.kind


@dataclass(frozen=True)
class Frame:
    """A physical ``ROWS`` frame (offsets count rows, never values)."""

    start: FrameBound
    end: FrameBound

    def __str__(self) -> str:
        return f"ROWS BETWEEN {self.start} AND {self.end}"


@dataclass(frozen=True)
class OrderKey:
    """One ORDER BY column with direction and explicit null placement."""

    column: str
    ascending: bool = True
    # None => SQL default (LAST when ascending, FIRST when descending).
    nulls_first: bool | None = None

    def effective_nulls_first(self) -> bool:
        if self.nulls_first is not None:
            return self.nulls_first
        # SQL:2008 / PostgreSQL default.
        return not self.ascending

    def signature(self) -> tuple[str, bool, bool]:
        """Identity used for exact-match comparisons."""
        return (self.column, self.ascending, self.effective_nulls_first())

    def sql(self) -> str:
        direction = "ASC" if self.ascending else "DESC"
        nulls = "NULLS FIRST" if self.effective_nulls_first() else "NULLS LAST"
        return f"{self.column} {direction} {nulls}"


def _normalize_partition(partition_by: tuple[str, ...]) -> tuple[str, ...]:
    # Multiset semantics: order-insensitive, duplicates preserved.
    return tuple(sorted(partition_by))


@dataclass(frozen=True)
class WindowSpec:
    """A named window definition (``func OVER w`` where ``w`` is one spec).

    ``func`` is one of ``row_number``, ``rank``, ``dense_rank``, ``sum``,
    ``count`` or ``avg``.  The function does not affect sort reuse; only the
    partition/order keys do.
    """

    name: str
    func: str = "row_number"
    partition_by: tuple[str, ...] = ()
    order_by: tuple[OrderKey, ...] = ()
    frame: Frame | None = None  # None -> default frame for the function
    value_column: str | None = None  # column aggregated by sum/count/avg

    def __post_init__(self) -> None:
        if not self.name:
            raise ValueError("window name must be non-empty")
        allowed = ("row_number", "rank", "dense_rank", "sum", "count", "avg")
        if self.func not in allowed:
            raise ValueError(f"unsupported window function: {self.func}")
        if self.frame is not None and not self.order_by:
            raise ValueError("explicit ROWS frame requires ORDER BY")
        if self.func in ("sum", "avg") and self.value_column is None:
            raise ValueError(f"{self.func} requires a value_column")
        # Multiset semantics for PARTITION BY; ORDER BY keeps declared order.
        object.__setattr__(self, "partition_by", _normalize_partition(tuple(self.partition_by)))
        object.__setattr__(self, "order_by", tuple(self.order_by))

    def partition_signature(self) -> tuple[str, ...]:
        return self.partition_by

    def order_signature(self) -> tuple[tuple[str, bool, bool], ...]:
        return tuple(key.signature() for key in self.order_by)

    def effective_frame(self) -> Frame:
        if self.frame is not None:
            return self.frame
        if self.order_by:
            # Standard default for aggregates with ORDER BY.
            return Frame(FrameBound(UNBOUNDED_PRECEDING), FrameBound(CURRENT_ROW))
        # No ORDER BY => the whole partition is the frame.
        return Frame(FrameBound(UNBOUNDED_PRECEDING), FrameBound(UNBOUNDED_FOLLOWING))


# Convenience constructors -------------------------------------------------

def unbounded_preceding() -> FrameBound:
    return FrameBound(UNBOUNDED_PRECEDING)


def preceding(offset: int) -> FrameBound:
    return FrameBound(PRECEDING, offset)


def current_row() -> FrameBound:
    return FrameBound(CURRENT_ROW)


def following(offset: int) -> FrameBound:
    return FrameBound(FOLLOWING, offset)


def unbounded_following() -> FrameBound:
    return FrameBound(UNBOUNDED_FOLLOWING)
