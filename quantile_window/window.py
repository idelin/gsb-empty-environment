from __future__ import annotations

from dataclasses import dataclass
import heapq
import math
import random
from typing import Any, Hashable, Optional, Tuple


Identity = Tuple[Hashable, Hashable]


@dataclass(frozen=True, slots=True)
class Event:
    """An observed value at ``time`` with a globally supplied identity."""

    time: float
    source: Hashable
    seq: Hashable
    value: Any

    @property
    def identity(self) -> Identity:
        return (self.source, self.seq)


@dataclass(frozen=True, slots=True)
class AdvanceResult:
    activated_events: int
    expired_active_events: int
    discarded_late_events: int
    stale_entries_popped: int


@dataclass(frozen=True, slots=True)
class WindowStats:
    active_events: int
    pending_events: int
    stored_events: int
    distinct_active_values: int
    treap_nodes: int
    active_heap_entries: int
    pending_heap_entries: int
    identity_tombstones: int
    ingest_accepted: int
    ingest_duplicates: int
    active_retractions: int
    pending_retractions: int
    expired_active_events: int
    discarded_late_events: int
    advances: int


class _Node:
    __slots__ = ("key", "priority", "count", "size", "left", "right")

    def __init__(self, key: Any, priority: int) -> None:
        self.key = key
        self.count = 1
        self.size = 1
        self.priority = priority
        self.left: Optional[_Node] = None
        self.right: Optional[_Node] = None


def _node_size(node: Optional[_Node]) -> int:
    return node.size if node is not None else 0


class _CountTreap:
    """Randomized balanced ordered tree; equal keys share one node."""

    def __init__(self, seed: Optional[int] = None) -> None:
        self._rng = random.Random(seed)
        self._root: Optional[_Node] = None
        self._nodes = 0

    def _rotate_right(self, node: _Node) -> _Node:
        child = node.left
        assert child is not None
        node.left = child.right
        child.right = node
        child.size = node.size
        node.size = _node_size(node.left) + node.count + _node_size(node.right)
        return child

    def _rotate_left(self, node: _Node) -> _Node:
        child = node.right
        assert child is not None
        node.right = child.left
        child.left = node
        child.size = node.size
        node.size = _node_size(node.left) + node.count + _node_size(node.right)
        return child

    def insert(self, key: Any) -> None:
        def insert_node(node: Optional[_Node]) -> _Node:
            if node is None:
                self._nodes += 1
                return _Node(key, self._rng.getrandbits(64))
            if key == node.key:
                node.count += 1
            elif key < node.key:
                node.left = insert_node(node.left)
                if node.left.priority < node.priority:
                    node = self._rotate_right(node)
            else:
                node.right = insert_node(node.right)
                if node.right.priority < node.priority:
                    node = self._rotate_left(node)
            node.size = _node_size(node.left) + node.count + _node_size(node.right)
            return node

        self._root = insert_node(self._root)

    def remove(self, key: Any) -> bool:
        found = False

        def remove_node(node: Optional[_Node]) -> Optional[_Node]:
            nonlocal found
            if node is None:
                return None
            if key == node.key:
                found = True
                if node.count > 1:
                    node.count -= 1
                else:
                    self._nodes -= 1
                    if node.left is None:
                        return node.right
                    if node.right is None:
                        return node.left
                    if node.left.priority < node.right.priority:
                        node = self._rotate_right(node)
                        node.right = remove_node(node.right)
                    else:
                        node = self._rotate_left(node)
                        node.left = remove_node(node.left)
            elif key < node.key:
                node.left = remove_node(node.left)
            else:
                node.right = remove_node(node.right)

            if node is not None:
                node.size = _node_size(node.left) + node.count + _node_size(node.right)
            return node

        self._root = remove_node(self._root)
        return found

    def kth(self, index: int) -> Any:
        if index < 0 or index >= len(self):
            raise IndexError("quantile index is outside the active window")
        node = self._root
        while node is not None:
            left_size = _node_size(node.left)
            if index < left_size:
                node = node.left
            elif index < left_size + node.count:
                return node.key
            else:
                index -= left_size + node.count
                node = node.right
        raise IndexError("quantile index is outside the active window")

    def __len__(self) -> int:
        return _node_size(self._root)

    @property
    def node_count(self) -> int:
        return self._nodes


class ExactQuantileWindow:
    """
    Exact dynamic quantiles for a left-open, right-closed sliding window.

    The active interval after a watermark ``w`` is ``(w - window_size, w]``.
    Retractions remove one particular ``(source, seq)`` event; equal values in
    the order-statistics tree occupy separate rank positions.
    """

    def __init__(self, window_size: float, *, treap_seed: Optional[int] = None) -> None:
        if window_size < 0:
            raise ValueError("window_size must be non-negative")
        self._window_size = window_size
        self._watermark: Optional[float] = None
        self._treap = _CountTreap(treap_seed)

        self._active_heap: list[tuple[float, int, Event]] = []
        self._pending_heap: list[tuple[float, int, Event]] = []
        self._heap_token = 0
        self._records: dict[Identity, tuple[Event, bool]] = {}
        self._known_identities: set[Identity] = set()

        self._active_count = 0
        self._pending_count = 0
        self._ingest_accepted = 0
        self._ingest_duplicates = 0
        self._active_retractions = 0
        self._pending_retractions = 0
        self._expired_active_events = 0
        self._discarded_late_events = 0
        self._advances = 0

    @property
    def window_size(self) -> float:
        return self._window_size

    @property
    def watermark(self) -> Optional[float]:
        return self._watermark

    def _push_heap(self, heap: list[tuple[float, int, Event]], event: Event) -> None:
        heapq.heappush(heap, (event.time, self._heap_token, event))
        self._heap_token += 1

    def ingest(self, event: Event) -> bool:
        """Accept a previously unseen event identity; return ``False`` on duplicate."""
        if not isinstance(event, Event):
            raise TypeError("event must be an Event")
        identity = event.identity
        if identity in self._known_identities:
            self._ingest_duplicates += 1
            return False

        self._known_identities.add(identity)
        self._ingest_accepted += 1

        if self._watermark is None or event.time > self._watermark:
            self._records[identity] = (event, False)
            self._push_heap(self._pending_heap, event)
            self._pending_count += 1
        elif event.time > self._watermark - self._window_size:
            self._records[identity] = (event, True)
            self._treap.insert(event.value)
            self._push_heap(self._active_heap, event)
            self._active_count += 1
        else:
            self._discarded_late_events += 1
        return True

    def retract(self, source: Hashable, seq: Hashable) -> bool:
        """Retract one event identity. A repeat retraction returns ``False``."""
        identity = (source, seq)
        record = self._records.pop(identity, None)
        if record is None:
            return False
        event, is_active = record
        if is_active:
            self._treap.remove(event.value)
            self._active_count -= 1
            self._active_retractions += 1
        else:
            self._pending_count -= 1
            self._pending_retractions += 1
        return True

    def advance_watermark(self, watermark: float) -> AdvanceResult:
        if self._watermark is not None and watermark < self._watermark:
            raise ValueError("watermark cannot move backwards")
        self._watermark = watermark
        self._advances += 1
        left = watermark - self._window_size

        expired = 0
        stale = 0
        while self._active_heap and self._active_heap[0][0] <= left:
            _, _, event = heapq.heappop(self._active_heap)
            record = self._records.pop(event.identity, None)
            if record is None or not record[1]:
                stale += 1
                continue
            self._treap.remove(event.value)
            self._active_count -= 1
            self._expired_active_events += 1
            expired += 1

        activated = 0
        discarded = 0
        while self._pending_heap and self._pending_heap[0][0] <= watermark:
            _, _, event = heapq.heappop(self._pending_heap)
            record = self._records.get(event.identity)
            if record is None:
                stale += 1
                continue
            stored_event, is_active = record
            if is_active or stored_event is not event:
                stale += 1
                continue
            if event.time <= left:
                del self._records[event.identity]
                self._pending_count -= 1
                self._discarded_late_events += 1
                discarded += 1
            else:
                self._records[event.identity] = (event, True)
                self._treap.insert(event.value)
                self._push_heap(self._active_heap, event)
                self._pending_count -= 1
                self._active_count += 1
                activated += 1

        return AdvanceResult(
            activated_events=activated,
            expired_active_events=expired,
            discarded_late_events=discarded,
            stale_entries_popped=stale,
        )

    def quantile(self, q: float) -> Any:
        """Return ``values[floor(q * (n - 1))]`` after sorting active values."""
        if self._active_count == 0:
            return None
        if q < 0 or q > 1:
            raise ValueError("q must be in [0, 1]")
        index = math.floor(q * (self._active_count - 1))
        return self._treap.kth(index)

    def stats(self) -> WindowStats:
        return WindowStats(
            active_events=self._active_count,
            pending_events=self._pending_count,
            stored_events=len(self._records),
            distinct_active_values=self._treap.node_count,
            treap_nodes=self._treap.node_count,
            active_heap_entries=len(self._active_heap),
            pending_heap_entries=len(self._pending_heap),
            identity_tombstones=len(self._known_identities),
            ingest_accepted=self._ingest_accepted,
            ingest_duplicates=self._ingest_duplicates,
            active_retractions=self._active_retractions,
            pending_retractions=self._pending_retractions,
            expired_active_events=self._expired_active_events,
            discarded_late_events=self._discarded_late_events,
            advances=self._advances,
        )


class NaiveSortingWindow:
    """Reference oracle that materializes and sorts active values on each query."""

    def __init__(self, window_size: float) -> None:
        if window_size < 0:
            raise ValueError("window_size must be non-negative")
        self._window_size = window_size
        self._watermark: Optional[float] = None
        self._records: dict[Identity, tuple[Event, bool]] = {}
        self._known_identities: set[Identity] = set()

    @property
    def watermark(self) -> Optional[float]:
        return self._watermark

    def ingest(self, event: Event) -> bool:
        if not isinstance(event, Event):
            raise TypeError("event must be an Event")
        identity = event.identity
        if identity in self._known_identities:
            return False
        self._known_identities.add(identity)

        if self._watermark is None or event.time > self._watermark:
            self._records[identity] = (event, False)
        elif event.time > self._watermark - self._window_size:
            self._records[identity] = (event, True)
        return True

    def retract(self, source: Hashable, seq: Hashable) -> bool:
        return self._records.pop((source, seq), None) is not None

    def advance_watermark(self, watermark: float) -> AdvanceResult:
        if self._watermark is not None and watermark < self._watermark:
            raise ValueError("watermark cannot move backwards")
        old_watermark = self._watermark
        self._watermark = watermark
        left = watermark - self._window_size
        retained: dict[Identity, tuple[Event, bool]] = {}
        activated = 0
        expired = 0
        discarded = 0

        for event, was_active in self._records.values():
            if left < event.time <= watermark:
                retained[event.identity] = (event, True)
                if old_watermark is None or not was_active:
                    activated += 1
            elif event.time > watermark:
                retained[event.identity] = (event, False)
            elif was_active:
                expired += 1
            else:
                discarded += 1

        self._records = retained
        return AdvanceResult(
            activated_events=activated,
            expired_active_events=expired,
            discarded_late_events=discarded,
            stale_entries_popped=0,
        )

    def quantile(self, q: float) -> Any:
        values = sorted(
            event.value for event, is_active in self._records.values() if is_active
        )
        if not values:
            return None
        if q < 0 or q > 1:
            raise ValueError("q must be in [0, 1]")
        return values[math.floor(q * (len(values) - 1))]

    def stats(self) -> WindowStats:
        active = sum(1 for _, is_active in self._records.values() if is_active)
        pending = len(self._records) - active
        return WindowStats(
            active_events=active,
            pending_events=pending,
            stored_events=len(self._records),
            distinct_active_values=len({e.value for e, active in self._records.values() if active}),
            treap_nodes=0,
            active_heap_entries=active,
            pending_heap_entries=pending,
            identity_tombstones=len(self._known_identities),
            ingest_accepted=0,
            ingest_duplicates=0,
            active_retractions=0,
            pending_retractions=0,
            expired_active_events=0,
            discarded_late_events=0,
            advances=0,
        )
