"""Exact quantiles over a sliding time window with arbitrary retraction.

Window semantics: left-open, right-closed. ``advance(w)`` evicts every event
with ``time <= w``; events with ``time == w`` are removed, events with
``time > w`` are kept.

Quantile: for the n sorted values, q-quantile is the element at index
``floor(q * (n - 1))`` (0-based). Empty window -> None.

Events are deduplicated by identity ``(source, seq)``; retraction removes by
identity, never by value, so duplicate values each keep their own slot.

Maintenance structure: a treap (randomized BST) keyed by ``(value, tag)``
with subtree sizes, giving O(log n) expected insert / delete / select-by-rank.
Advancing the watermark costs O(k log n) for k expired events -- the window
is never re-sorted. A min-heap on event time drives batch expiry with lazy
deletion of already-retracted entries.
"""

from __future__ import annotations

import heapq
import math
import random
from typing import Any, Hashable, Optional, Tuple

EventId = Tuple[Hashable, Hashable]


class _Node:
    __slots__ = ("key", "prio", "left", "right", "size")

    def __init__(self, key: tuple, prio: int) -> None:
        self.key = key            # (value, tag), tag unique per event
        self.prio = prio
        self.left: Optional[_Node] = None
        self.right: Optional[_Node] = None
        self.size = 1


def _size(node: Optional[_Node]) -> int:
    return node.size if node is not None else 0


def _update(node: _Node) -> None:
    node.size = 1 + _size(node.left) + _size(node.right)


def _split(root: Optional[_Node], key: tuple) -> tuple:
    """Split into (< key, >= key)."""
    if root is None:
        return None, None
    if key <= root.key:
        left, right = _split(root.left, key)
        root.left = right
        _update(root)
        return left, root
    left, right = _split(root.right, key)
    root.right = left
    _update(root)
    return root, right


def _merge(a: Optional[_Node], b: Optional[_Node]) -> Optional[_Node]:
    if a is None:
        return b
    if b is None:
        return a
    if a.prio < b.prio:
        a.right = _merge(a.right, b)
        _update(a)
        return a
    b.left = _merge(a, b.left)
    _update(b)
    return b


def _insert(root: Optional[_Node], node: _Node) -> _Node:
    left, right = _split(root, node.key)
    return _merge(_merge(left, node), right)


def _delete(root: Optional[_Node], key: tuple) -> Optional[_Node]:
    # Keys are unique (value, tag) pairs; the successor key of (v, t) is
    # (v, t + 1), so the middle segment holds exactly the target node.
    left, ge = _split(root, key)
    _eq, right = _split(ge, (key[0], key[1] + 1))
    return _merge(left, right)


def _select(root: _Node, rank: int) -> tuple:
    """Return the key at 0-based ``rank`` in in-order order."""
    node = root
    while node is not None:
        left_size = _size(node.left)
        if rank < left_size:
            node = node.left
        elif rank == left_size:
            return node.key
        else:
            rank -= left_size + 1
            node = node.right
    raise IndexError(rank)


class SlidingQuantileWindow:
    """Exact q-quantile queries over a left-open, right-closed time window."""

    def __init__(self) -> None:
        self._events: dict[EventId, tuple] = {}  # (source, seq) -> (time, value, tag)
        self._heap: list = []                    # (time, tag, source, seq), lazy
        self._root: Optional[_Node] = None
        self._next_tag = 0
        self._watermark: Optional[float] = None

    def __len__(self) -> int:
        return len(self._events)

    @property
    def watermark(self) -> Optional[float]:
        return self._watermark

    def ingest(self, source: Hashable, seq: Hashable, time: float, value: Any) -> bool:
        """Add an event. Returns False (no-op) if (source, seq) already exists."""
        event_id = (source, seq)
        if event_id in self._events:
            return False
        tag = self._next_tag
        self._next_tag += 1
        self._events[event_id] = (time, value, tag)
        heapq.heappush(self._heap, (time, tag, source, seq))
        self._root = _insert(self._root, _Node((value, tag), random.getrandbits(64)))
        return True

    def retract(self, source: Hashable, seq: Hashable) -> bool:
        """Remove an event by identity. Returns False if it is not present."""
        record = self._events.pop((source, seq), None)
        if record is None:
            return False
        _time, value, tag = record
        self._root = _delete(self._root, (value, tag))
        # The heap entry is left behind and skipped lazily on expiry.
        return True

    def advance(self, watermark: float) -> int:
        """Evict all events with time <= watermark. Returns the evicted count."""
        evicted = 0
        heap = self._heap
        while heap and heap[0][0] <= watermark:
            _time, tag, source, seq = heapq.heappop(heap)
            record = self._events.get((source, seq))
            if record is None or record[2] != tag:
                continue  # already retracted
            del self._events[(source, seq)]
            self._root = _delete(self._root, (record[1], tag))
            evicted += 1
        if self._watermark is None or watermark > self._watermark:
            self._watermark = watermark
        return evicted

    def quantile(self, q: float) -> Optional[Any]:
        """Exact q-quantile: sorted values, index floor(q * (n - 1))."""
        if not 0.0 <= q <= 1.0:
            raise ValueError(f"q must be in [0, 1], got {q!r}")
        n = len(self._events)
        if n == 0:
            return None
        rank = math.floor(q * (n - 1))
        rank = min(max(rank, 0), n - 1)
        return _select(self._root, rank)[0]


class NaiveQuantileOracle:
    """Reference implementation: re-sorts the whole window on every query."""

    def __init__(self) -> None:
        self._events: dict[EventId, tuple] = {}  # (source, seq) -> (time, value)

    def __len__(self) -> int:
        return len(self._events)

    def ingest(self, source: Hashable, seq: Hashable, time: float, value: Any) -> bool:
        event_id = (source, seq)
        if event_id in self._events:
            return False
        self._events[event_id] = (time, value)
        return True

    def retract(self, source: Hashable, seq: Hashable) -> bool:
        return self._events.pop((source, seq), None) is not None

    def advance(self, watermark: float) -> int:
        stale = [eid for eid, (t, _v) in self._events.items() if t <= watermark]
        for eid in stale:
            del self._events[eid]
        return len(stale)

    def quantile(self, q: float) -> Optional[Any]:
        if not 0.0 <= q <= 1.0:
            raise ValueError(f"q must be in [0, 1], got {q!r}")
        n = len(self._events)
        if n == 0:
            return None
        values = sorted(v for _t, v in self._events.values())
        rank = min(max(math.floor(q * (n - 1)), 0), n - 1)
        return values[rank]
