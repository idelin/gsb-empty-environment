"""Bitemporal index: locate evidence by object and both time coordinates.

Structure per object
--------------------
* versions kept sorted by the explicit priority key ``(tx_from, seq)``;
* a lazily compiled, cached grid: for every distinct ``tx_from`` breakpoint
  the business-time axis is resolved into non-overlapping ``Slab``s using
  only the versions visible in that transaction-time slab.

A point query is then two binary searches: pick the transaction-time slab
containing ``t_tx`` (visibility prefix), then the valid-time slab containing
``t_valid``.  The grid is rebuilt for an object only when a new version for
that object arrives; it is a derived structure and can always be recomputed
from the store.
"""

from __future__ import annotations

import sys
from bisect import bisect_right
from dataclasses import dataclass
from typing import Any, Iterable, Iterator

from .model import Version


@dataclass(frozen=True)
class Slab:
    """A resolved piece of the business-time axis for one tx-time slab."""

    valid_from: float
    valid_to: float
    version: Version | None   # None = gap between recorded intervals


def resolve_timeline(visible: list[Version]) -> list[Slab]:
    """Resolve a set of mutually visible versions into non-overlapping slabs.

    ``visible`` must be sorted by ascending priority; the last (highest
    priority) version covering an elementary interval wins.  Adjacent
    elementary intervals with the same winner are merged.
    """
    if not visible:
        return []
    points = sorted({p for v in visible for p in (v.valid_from, v.valid_to)})
    slabs: list[Slab] = []
    for lo, hi in zip(points, points[1:]):
        winner: Version | None = None
        for v in visible:  # ascending priority: last covering wins
            if v.valid_from <= lo and v.valid_to >= hi:
                winner = v
        if slabs and slabs[-1].version is winner and slabs[-1].valid_to == lo:
            slabs[-1] = Slab(slabs[-1].valid_from, hi, winner)
        else:
            slabs.append(Slab(lo, hi, winner))
    return slabs


class BitemporalIndex:
    def __init__(self, versions: Iterable[Version] = ()) -> None:
        # object_id -> versions sorted by priority (tx_from, seq)
        self._by_object: dict[str, list[Version]] = {}
        # object_id -> (tx breakpoints, resolved slab list per breakpoint)
        self._grid: dict[str, tuple[list[float], list[list[Slab]]]] = {}
        self.add_all(versions)

    # -- construction -----------------------------------------------------

    def add(self, v: Version) -> None:
        vs = self._by_object.setdefault(v.object_id, [])
        i = bisect_right(vs, v.priority, key=lambda x: x.priority)
        if i > 0 and vs[i - 1].priority == v.priority:
            raise ValueError(
                f"duplicate priority {v.priority} for object {v.object_id!r}"
            )
        vs.insert(i, v)
        self._grid.pop(v.object_id, None)  # invalidate derived grid

    def add_all(self, versions: Iterable[Version]) -> None:
        for v in versions:
            self.add(v)

    def objects(self) -> Iterator[str]:
        return iter(self._by_object)

    # -- queries ------------------------------------------------------------

    def _compiled(
        self, object_id: str
    ) -> tuple[list[float], list[list[Slab]]]:
        hit = self._grid.get(object_id)
        if hit is not None:
            return hit
        vs = self._by_object.get(object_id, [])
        tx_points = sorted({v.tx_from for v in vs})
        grids: list[list[Slab]] = []
        for p in tx_points:
            # visible prefix: priority <= (p, +inf)
            end = bisect_right(vs, (p, sys.maxsize), key=lambda x: x.priority)
            grids.append(resolve_timeline(vs[:end]))
        compiled = (tx_points, grids)
        self._grid[object_id] = compiled
        return compiled

    def query(
        self, object_id: str, t_valid: float, t_tx: float
    ) -> Version | None:
        """Evidence for ``object_id`` at business time ``t_valid`` as known
        at observation time ``t_tx``; None when nothing was recorded."""
        tx_points, grids = self._compiled(object_id)
        i = bisect_right(tx_points, t_tx) - 1
        if i < 0:
            return None
        slabs = grids[i]
        j = bisect_right(slabs, t_valid, key=lambda s: s.valid_from) - 1
        if j < 0:
            return None
        slab = slabs[j]
        if slab.valid_from <= t_valid < slab.valid_to:
            return slab.version
        return None

    def value_at(self, object_id: str, t_valid: float, t_tx: float) -> Any:
        v = self.query(object_id, t_valid, t_tx)
        return None if v is None or v.is_tombstone else v.value

    def timeline(self, object_id: str, t_tx: float) -> list[Slab]:
        """The whole resolved business-time axis as known at ``t_tx``
        ("截至某观察时刻，发生了什么")."""
        tx_points, grids = self._compiled(object_id)
        i = bisect_right(tx_points, t_tx) - 1
        return list(grids[i]) if i >= 0 else []

    def knowledge(
        self, object_id: str, t_valid: float, observations: Iterable[float]
    ) -> list[tuple[float, Version | None]]:
        """The transaction-time axis for one business instant: what was
        known about ``t_valid`` at each observation moment
        ("当时我们知道什么")."""
        return [(tx, self.query(object_id, t_valid, tx)) for tx in observations]
