"""Event store: late arrivals, corrections, retractions, snapshot queries.

Every mutation appends a new tree version (path-copying, O(log N)); old
versions remain queryable.  Business-bucket rules are versioned
independently in a ``RuleRegistry`` -- changing the rule never rebuilds
the tree.

Range counts decompose into *complete internal fine buckets* (answered
from aggregate tree nodes) and *partial edge buckets* (itemised event by
event from leaf storage).  The two parts are disjoint by construction, so
nothing is double counted.  ``explain_range`` traces every contribution
back to its source event id and version.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple

from .bucketing import BucketRule, FineBuckets, RuleRegistry
from .ptree import Item, Node, PersistentCountTree
from .timeaxis import Instant


@dataclass(frozen=True)
class Contribution:
    """One recorded mutation: who contributed what, and which version."""

    event_id: str
    version: int
    ticks: int
    kind: str  # "ingest" | "correct" | "retract"


@dataclass(frozen=True)
class CountResult:
    """Range-count answer with its internal/edge decomposition."""

    total: int
    internal: int  # events in complete internal fine buckets
    edge_items: Tuple[Item, ...]  # itemised events from partial edge buckets

    def __post_init__(self):
        if self.total != self.internal + len(self.edge_items):
            raise ValueError("internal + edge must equal total (no double count)")


class EventStore:
    def __init__(self, fine: FineBuckets):
        self.fine = fine
        self.rules = RuleRegistry(fine)
        self._tree = PersistentCountTree()
        self._roots: List[Optional[Node]] = [None]  # version 0: empty
        self._live: Dict[str, Item] = {}  # event_id -> current item
        self._history: List[Contribution] = []

    # ------------------------------------------------------------------ mutations

    @property
    def snapshot_version(self) -> int:
        """Version number of the current tree state."""
        return len(self._roots) - 1

    def root_at(self, version: Optional[int] = None) -> Optional[Node]:
        return self._roots[self.snapshot_version if version is None else version]

    def _apply(self, item: Item, delta: int) -> None:
        index = self.fine.index(item[0])
        self._roots.append(self._tree.update(self._roots[-1], index, item, delta))

    def ingest(self, event_id: str, instant: Instant, version: int = 1) -> None:
        """Add an event; late (out-of-order) arrivals are fine."""
        if event_id in self._live:
            raise ValueError(f"{event_id!r} already live; use correct()/retract()")
        item = (instant.ticks, event_id, version)
        self._live[event_id] = item
        self._apply(item, +1)
        self._history.append(Contribution(event_id, version, item[0], "ingest"))

    def correct(self, event_id: str, instant: Instant, version: int) -> None:
        """Replace an event with a corrected version (supersedes the old one)."""
        old = self._live.get(event_id)
        if old is None:
            raise ValueError(f"{event_id!r} is not live")
        if version <= old[2]:
            raise ValueError("correction version must increase")
        new = (instant.ticks, event_id, version)
        self._apply(old, -1)  # remove old contribution: one event, one leaf
        self._apply(new, +1)
        self._live[event_id] = new
        self._history.append(Contribution(event_id, version, new[0], "correct"))

    def retract(self, event_id: str) -> None:
        """Withdraw an event entirely."""
        old = self._live.pop(event_id, None)
        if old is None:
            raise ValueError(f"{event_id!r} is not live")
        self._apply(old, -1)
        self._history.append(Contribution(event_id, old[2], old[0], "retract"))

    @property
    def history(self) -> Tuple[Contribution, ...]:
        return tuple(self._history)

    # ------------------------------------------------------------------ queries

    def total(self, version: Optional[int] = None) -> int:
        root = self.root_at(version)
        return root.count if root is not None else 0

    def count_range(
        self, lo: Instant, hi: Instant, version: Optional[int] = None
    ) -> CountResult:
        """Exact count in [lo, hi): internal aggregates + itemised edges."""
        root = self.root_at(version)
        if lo.ticks >= hi.ticks:
            return CountResult(0, 0, ())
        fine = self.fine
        b_lo, b_hi = fine.index(lo.ticks), fine.index(hi.ticks)
        lo_aligned = fine.bounds(b_lo)[0] == lo.ticks
        hi_aligned = fine.bounds(b_hi)[0] == hi.ticks

        edge: List[Item] = []
        if b_lo == b_hi and not (lo_aligned and hi_aligned):
            # Whole range inside a single partial bucket: itemise it.
            edge = [it for it in self._tree.leaf_items(root, b_lo)
                    if lo.ticks <= it[0] < hi.ticks]
            return CountResult(len(edge), 0, tuple(edge))

        internal_lo, internal_hi = b_lo, b_hi
        if not lo_aligned:
            edge.extend(it for it in self._tree.leaf_items(root, b_lo)
                        if it[0] >= lo.ticks)
            internal_lo = b_lo + 1
        if not hi_aligned:
            edge.extend(it for it in self._tree.leaf_items(root, b_hi)
                        if it[0] < hi.ticks)
        internal = self._tree.count(root, internal_lo, internal_hi)
        return CountResult(internal + len(edge), internal, tuple(edge))

    def kth(self, k: int, version: Optional[int] = None) -> Item:
        """0-based k-th event; duplicate times ordered by stable event id."""
        return self._tree.kth(self.root_at(version), k)

    def quantile(self, q: float, version: Optional[int] = None) -> Optional[Instant]:
        """Smallest time t with (# events <= t) >= ceil(q * n)."""
        if not 0.0 <= q <= 1.0:
            raise ValueError("q must be in [0, 1]")
        n = self.total(version)
        if n == 0:
            return None
        k = max(0, min(n - 1, math.ceil(q * n) - 1))
        return Instant(self.kth(k, version)[0])

    def business_counts(
        self, rule: Optional[BucketRule] = None, version: Optional[int] = None
    ) -> List[int]:
        """Per-business-bucket counts under a (possibly historic) rule."""
        rule = self.rules.current if rule is None else rule
        root = self.root_at(version)
        return [self._tree.count(root, lo, hi) for lo, hi in rule.fine_ranges()]

    def explain_range(
        self, lo: Instant, hi: Instant, version: Optional[int] = None
    ) -> List[Item]:
        """All contributing (ticks, event_id, version) items in [lo, hi)."""
        root = self.root_at(version)
        fine = self.fine
        first, last = fine.index(lo.ticks), fine.index(hi.ticks)
        items: List[Item] = []
        for index in range(first, last + 1):
            items.extend(it for it in self._tree.leaf_items(root, index)
                         if lo.ticks <= it[0] < hi.ticks)
        items.sort()
        return items
