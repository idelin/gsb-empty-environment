"""Core data model: edges and the batch operation vocabulary."""

from __future__ import annotations

from dataclasses import dataclass

NEG_INF = float("-inf")
POS_INF = float("inf")


@dataclass
class Edge:
    """A directed multi-edge with a half-open activity interval [start, end).

    ``None`` bounds are open ended.  An edge is active at time ``t`` iff
    ``start <= t < end`` (left-closed, right-open).
    """

    eid: str
    src: str
    dst: str
    start: float | None = None
    end: float | None = None

    def active_at(self, t: float) -> bool:
        lo = NEG_INF if self.start is None else self.start
        hi = POS_INF if self.end is None else self.end
        return lo <= t < hi


# ---------------------------------------------------------------------------
# Batch operations.  All ops are immutable value objects applied via
# ``Projection.apply``; a batch is validated and committed atomically.
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class AddVertex:
    vid: str
    group: str | None = None  # None => root level


@dataclass(frozen=True)
class AddGroup:
    gid: str
    parent: str | None = None


@dataclass(frozen=True)
class AddEdge:
    eid: str
    src: str
    dst: str
    start: float | None = None
    end: float | None = None


@dataclass(frozen=True)
class MoveVertex:
    vid: str
    group: str | None


@dataclass(frozen=True)
class MoveGroup:
    gid: str
    parent: str | None


@dataclass(frozen=True)
class Collapse:
    gid: str


@dataclass(frozen=True)
class Expand:
    gid: str
