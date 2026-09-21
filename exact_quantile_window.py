"""Exact quantiles over a sliding time window with arbitrary retraction.

Semantics
---------
* The window is left-open / right-closed on event time: an event is a member
  iff ``left < ts <= right`` where ``right`` is the current watermark and
  ``left = right - window``.
* The watermark only moves forward. ``ingest`` auto-advances it to the event
  timestamp when needed; ``advance`` moves it explicitly and evicts every
  event with ``ts <= left`` in one batch.
* Events are deduplicated by identity ``(source, seq)``; re-ingesting a live
  identity is a no-op. ``retract`` removes by identity, never by value, so
  duplicated values each keep their own slot.
* Quantile: for the ``n`` sorted in-window values, ``query(q)`` returns the
  value at 0-based index ``floor(q * (n - 1))``; an empty window yields
  ``None``. This is exact -- no sketching, no approximation.

Structure
---------
* Order-statistics treap keyed by ``(value, uid)``: O(log n) expected
  insert / erase / select-by-rank. Duplicates coexist because ``uid`` breaks
  ties; retraction targets the exact node of one event identity.
* ``(source, seq) -> (ts, value, uid)`` dict for dedup and retraction lookup.
* Min-heap of ``(ts, uid, source, seq)`` for batch expiry: ``advance`` pops
  all expired entries at O(k log n) for k evictions instead of rescanning or
  re-sorting the window. Entries of retracted events become stale and are
  skipped lazily when popped.

Costs
-----
* ingest:   O(log n) expected
* retract:  O(log n) expected
* query:    O(log n) expected
* advance:  O(k log n) expected for k expired events (k = 0 -> O(1))
* space:    O(n) live events, plus at most one stale heap entry per
            retracted-but-not-yet-expired event
"""

from __future__ import annotations

import heapq
import math
import random
import sys
from typing import Any, Hashable, Optional

__all__ = ["ExactQuantileWindow", "NaiveQuantileOracle"]


class _Node:
    __slots__ = ("key", "prio", "left", "right", "size")

    def __init__(self, key: tuple, prio: float) -> None:
        self.key = key          # (value, uid)
        self.prio = prio
        self.left: Optional[_Node] = None
        self.right: Optional[_Node] = None
        self.size = 1


def _sz(node: Optional[_Node]) -> int:
    return node.size if node is not None else 0


def _upd(node: _Node) -> None:
    node.size = 1 + _sz(node.left) + _sz(node.right)


def _rot_right(p: _Node) -> _Node:
    l = p.left
    p.left = l.right
    l.right = p
    _upd(p)
    _upd(l)
    return l


def _rot_left(p: _Node) -> _Node:
    r = p.right
    p.right = r.left
    r.left = p
    _upd(p)
    _upd(r)
    return r


def _insert(root: Optional[_Node], node: _Node) -> _Node:
    if root is None:
        return node
    if node.key < root.key:
        root.left = _insert(root.left, node)
        if root.left.prio < root.prio:
            root = _rot_right(root)
    else:
        root.right = _insert(root.right, node)
        if root.right.prio < root.prio:
            root = _rot_left(root)
    _upd(root)
    return root


def _merge(l: Optional[_Node], r: Optional[_Node]) -> Optional[_Node]:
    if l is None:
        return r
    if r is None:
        return l
    if l.prio < r.prio:
        l.right = _merge(l.right, r)
        _upd(l)
        return l
    r.left = _merge(l, r.left)
    _upd(r)
    return r


def _erase(root: Optional[_Node], key: tuple) -> Optional[_Node]:
    if root is None:
        return None
    if key < root.key:
        root.left = _erase(root.left, key)
    elif key > root.key:
        root.right = _erase(root.right, key)
    else:
        return _merge(root.left, root.right)
    _upd(root)
    return root


def _select(root: Optional[_Node], k: int) -> tuple:
    """Return the key at 0-based rank k (iterative, O(log n) expected)."""
    while root is not None:
        left_size = _sz(root.left)
        if k < left_size:
            root = root.left
        elif k == left_size:
            return root.key
        else:
            k -= left_size + 1
            root = root.right
    raise IndexError(k)


class ExactQuantileWindow:
    """Sliding-window exact quantiles with identity-based retraction."""

    def __init__(self, window: float) -> None:
        if window <= 0:
            raise ValueError("window must be positive")
        self.window = window
        self.right: Optional[float] = None   # watermark (right edge, closed)
        self.left: Optional[float] = None    # right - window (left edge, open)
        self._events: dict[tuple, tuple] = {}  # (source, seq) -> (ts, value, uid)
        self._root: Optional[_Node] = None
        self._size = 0
        self._expiry: list = []              # heap of (ts, uid, source, seq)
        self._next_uid = 0
        self._rng = random.Random()
        # counters for reporting
        self.total_ingested = 0
        self.total_expired = 0
        self.total_retracted = 0

    # ------------------------------------------------------------------ API

    def ingest(self, source: Hashable, seq: Hashable, ts: float, value: Any) -> bool:
        """Add an event. Returns False for duplicates or late arrivals."""
        ident = (source, seq)
        if ident in self._events:
            return False
        if self.left is not None and ts <= self.left:
            return False  # already outside the (left, right] window
        uid = self._next_uid
        self._next_uid += 1
        self._events[ident] = (ts, value, uid)
        self._root = _insert(self._root, _Node((value, uid), self._rng.random()))
        self._size += 1
        heapq.heappush(self._expiry, (ts, uid, source, seq))
        self.total_ingested += 1
        if self.right is None or ts > self.right:
            self._set_watermark(ts)
        return True

    def advance(self, ts: float) -> int:
        """Move the watermark to ``ts``; batch-evict ``ts <= left``. Returns evicted count."""
        if self.right is not None and ts < self.right:
            raise ValueError("watermark cannot move backward")
        if self.right == ts:
            return 0
        return self._set_watermark(ts)

    def retract(self, source: Hashable, seq: Hashable) -> bool:
        """Remove one event by identity. Returns False if it is not live."""
        rec = self._events.pop((source, seq), None)
        if rec is None:
            return False
        _, value, uid = rec
        self._root = _erase(self._root, (value, uid))
        self._size -= 1
        self.total_retracted += 1
        return True

    def query(self, q: float) -> Optional[Any]:
        """Exact q-quantile: sorted value at index floor(q*(n-1)); None if empty."""
        if not 0.0 <= q <= 1.0:
            raise ValueError("q must be in [0, 1]")
        n = self._size
        if n == 0:
            return None
        k = math.floor(q * (n - 1))
        return _select(self._root, k)[0]

    def __len__(self) -> int:
        return self._size

    # ------------------------------------------------------------- internals

    def _set_watermark(self, ts: float) -> int:
        self.right = ts
        self.left = ts - self.window
        evicted = 0
        expiry = self._expiry
        left = self.left
        while expiry and expiry[0][0] <= left:
            _, uid, source, seq = heapq.heappop(expiry)
            rec = self._events.pop((source, seq), None)
            if rec is None or rec[2] != uid:
                continue  # stale entry: event was retracted earlier
            self._root = _erase(self._root, (rec[1], uid))
            self._size -= 1
            evicted += 1
        self.total_expired += evicted
        return evicted

    def space_report(self) -> dict:
        """Estimated memory footprint of the maintenance structures."""
        sample_node = _Node((0.0, 0), 0.0)
        per_treap_node = sys.getsizeof(sample_node) + sys.getsizeof(sample_node.key)
        treap_bytes = per_treap_node * self._size

        events_bytes = sys.getsizeof(self._events)
        if self._events:
            ident, rec = next(iter(self._events.items()))
            per_event = sys.getsizeof(ident) + sys.getsizeof(rec)
            events_bytes += per_event * len(self._events)

        heap_bytes = sys.getsizeof(self._expiry)
        if self._expiry:
            heap_bytes += sys.getsizeof(self._expiry[0]) * len(self._expiry)

        stale_heap_entries = len(self._expiry) - self._size
        total = treap_bytes + events_bytes + heap_bytes
        return {
            "live_events": self._size,
            "treap_nodes": self._size,
            "heap_entries": len(self._expiry),
            "stale_heap_entries": stale_heap_entries,
            "treap_bytes": treap_bytes,
            "events_dict_bytes": events_bytes,
            "expiry_heap_bytes": heap_bytes,
            "total_bytes_estimate": total,
            "bytes_per_live_event": (total / self._size) if self._size else 0.0,
        }


class NaiveQuantileOracle:
    """Reference implementation: re-sorts the whole window on every query.

    Same external semantics as ExactQuantileWindow, deliberately dumb; used as
    the ground-truth oracle for randomized differential testing.
    """

    def __init__(self, window: float) -> None:
        if window <= 0:
            raise ValueError("window must be positive")
        self.window = window
        self.right: Optional[float] = None
        self._events: dict[tuple, tuple] = {}  # (source, seq) -> (ts, value)

    def ingest(self, source: Hashable, seq: Hashable, ts: float, value: Any) -> bool:
        ident = (source, seq)
        if ident in self._events:
            return False
        if self.right is not None and ts <= self.right - self.window:
            return False
        self._events[ident] = (ts, value)
        if self.right is None or ts > self.right:
            self.right = ts
            self._purge_expired()
        return True

    def advance(self, ts: float) -> int:
        if self.right is not None and ts < self.right:
            raise ValueError("watermark cannot move backward")
        if self.right != ts:
            self.right = ts
            self._purge_expired()
        return 0

    def retract(self, source: Hashable, seq: Hashable) -> bool:
        return self._events.pop((source, seq), None) is not None

    def _purge_expired(self) -> None:
        # Naive O(n) rescan per watermark move; mirrors what the treap-based
        # window evicts, so both expose identical live-set semantics.
        left = self.right - self.window
        self._events = {k: v for k, v in self._events.items() if v[0] > left}

    def _live_values(self) -> list:
        if self.right is None:
            return []
        left = self.right - self.window
        return sorted(v for (t, v) in self._events.values() if left < t <= self.right)

    def query(self, q: float) -> Optional[Any]:
        if not 0.0 <= q <= 1.0:
            raise ValueError("q must be in [0, 1]")
        vals = self._live_values()
        if not vals:
            return None
        return vals[math.floor(q * (len(vals) - 1))]

    def __len__(self) -> int:
        return len(self._live_values())
