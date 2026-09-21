from __future__ import annotations

import heapq
import math
import random
from dataclasses import dataclass
from typing import Any


_WAITING = 0
_ACTIVE = 1
_EXPIRED = 2
_RETRACTED = 3


@dataclass(slots=True)
class _Event:
    uid: int
    key: tuple[Any, Any]
    timestamp: Any
    value: Any
    status: int


class _Node:
    __slots__ = ("value", "priority", "left", "right", "count", "size")

    def __init__(self, value: Any, priority: int) -> None:
        self.value = value
        self.priority = priority
        self.left: _Node | None = None
        self.right: _Node | None = None
        self.count = 1
        self.size = 1


def _node_size(node: _Node | None) -> int:
    return node.size if node is not None else 0


def _update(node: _Node) -> None:
    node.size = _node_size(node.left) + node.count + _node_size(node.right)


def _add(
    root: _Node | None,
    value: Any,
    amount: int,
    rng: random.Random,
) -> tuple[_Node | None, bool]:
    if root is None:
        return _Node(value, rng.getrandbits(64)), True

    created = False
    if value == root.value:
        root.count += amount
    elif value < root.value:
        root.left, created = _add(root.left, value, amount, rng)
        if root.left.priority > root.priority:
            rotated = root.left
            root.left = rotated.right
            rotated.right = root
            _update(root)
            _update(rotated)
            return rotated, created
    else:
        root.right, created = _add(root.right, value, amount, rng)
        if root.right.priority > root.priority:
            rotated = root.right
            root.right = rotated.left
            rotated.left = root
            _update(root)
            _update(rotated)
            return rotated, created

    _update(root)
    return root, created


def _merge(left: _Node | None, right: _Node | None) -> _Node | None:
    if left is None:
        return right
    if right is None:
        return left

    if left.priority > right.priority:
        left.right = _merge(left.right, right)
        _update(left)
        return left

    right.left = _merge(left, right.left)
    _update(right)
    return right


def _remove(
    root: _Node | None,
    value: Any,
    amount: int,
) -> tuple[_Node | None, bool, bool]:
    if root is None:
        raise ValueError("value is not present in active window")

    removed = False
    eliminated = False
    if value == root.value:
        if amount > root.count:
            raise ValueError("value is not present in active window")
        root.count -= amount
        removed = True
        if root.count == 0:
            return _merge(root.left, root.right), removed, True
        eliminated = False
    elif value < root.value:
        root.left, removed, eliminated = _remove(root.left, value, amount)
    else:
        root.right, removed, eliminated = _remove(root.right, value, amount)

    _update(root)
    return root, removed, eliminated


def _kth(root: _Node | None, index: int) -> Any:
    current = root
    while current is not None:
        left_size = _node_size(current.left)
        if index < left_size:
            current = current.left
        elif index < left_size + current.count:
            return current.value
        else:
            index -= left_size + current.count
            current = current.right
    raise IndexError("quantile index is outside active window")


def _values_in_order(root: _Node | None) -> list[Any]:
    values: list[Any] = []

    def visit(node: _Node | None) -> None:
        if node is None:
            return
        visit(node.left)
        values.extend([node.value] * node.count)
        visit(node.right)

    visit(root)
    return values


class ExactQuantileWindow:
    """Fixed-width, left-open/right-closed event window with exact quantiles."""

    def __init__(self, window_size: float | int) -> None:
        if window_size <= 0:
            raise ValueError("window_size must be positive")
        self._window_size = window_size
        self._rng = random.Random()
        self._events: dict[tuple[Any, Any], _Event] = {}
        self._active_heap: list[tuple[Any, int, _Event]] = []
        self._waiting_heap: list[tuple[Any, int, _Event]] = []
        self._root: _Node | None = None
        self._uid = 0
        self._left: Any = None
        self._right: Any = None
        self._active_count = 0
        self._waiting_count = 0
        self._expired_count = 0
        self._retracted_count = 0
        self._distinct_count = 0

    def ingest(self, source: Any, seq: Any, timestamp: Any, value: Any) -> bool:
        """Add one event; return False only when its identity is a duplicate."""
        key = (source, seq)
        if key in self._events:
            return False

        if self._right is not None and timestamp <= self._left:
            raise ValueError("event timestamp is older than the current watermark")

        self._uid += 1
        is_active = self._right is not None and timestamp <= self._right
        event = _Event(
            self._uid,
            key,
            timestamp,
            value,
            _ACTIVE if is_active else _WAITING,
        )
        self._events[key] = event

        if is_active:
            self._root, created = _add(self._root, value, 1, self._rng)
            if created:
                self._distinct_count += 1
            self._active_count += 1
            heapq.heappush(self._active_heap, (timestamp, self._uid, event))
        else:
            self._waiting_count += 1
            heapq.heappush(self._waiting_heap, (timestamp, self._uid, event))

        return True

    def advance_watermark(self, timestamp: Any) -> None:
        """Advance the right edge; the left edge moves by the fixed width."""
        if self._right is not None and timestamp < self._right:
            raise ValueError("watermark cannot move backwards")

        self._right = timestamp
        self._left = timestamp - self._window_size

        expired_values: dict[Any, int] = {}
        while self._active_heap and self._active_heap[0][0] <= self._left:
            _, _, event = heapq.heappop(self._active_heap)
            if event.status == _ACTIVE:
                expired_values[event.value] = expired_values.get(event.value, 0) + 1
                event.status = _EXPIRED
                self._active_count -= 1
                self._expired_count += 1

        for value, amount in expired_values.items():
            self._root, removed, eliminated = _remove(self._root, value, amount)
            if not removed:
                raise RuntimeError("active value index became inconsistent")
            if eliminated:
                self._distinct_count -= 1

        while self._waiting_heap and self._waiting_heap[0][0] <= self._right:
            _, _, event = heapq.heappop(self._waiting_heap)
            if event.status == _RETRACTED:
                continue
            if event.status != _WAITING:
                raise RuntimeError("waiting event index became inconsistent")

            if event.timestamp <= self._left:
                event.status = _EXPIRED
                self._waiting_count -= 1
                self._expired_count += 1
            else:
                self._root, created = _add(self._root, event.value, 1, self._rng)
                if created:
                    self._distinct_count += 1
                event.status = _ACTIVE
                self._waiting_count -= 1
                self._active_count += 1
                heapq.heappush(
                    self._active_heap,
                    (event.timestamp, event.uid, event),
                )

    def retract(self, source: Any, seq: Any) -> bool:
        """Retract one event identity; return False if unknown or already retracted."""
        event = self._events.get((source, seq))
        if event is None or event.status == _RETRACTED:
            return False

        if event.status == _ACTIVE:
            self._root, removed, eliminated = _remove(self._root, event.value, 1)
            if not removed:
                raise RuntimeError("active value index became inconsistent")
            if eliminated:
                self._distinct_count -= 1
            self._active_count -= 1
        elif event.status == _WAITING:
            self._waiting_count -= 1
        elif event.status == _EXPIRED:
            self._expired_count -= 1

        event.status = _RETRACTED
        self._retracted_count += 1
        return True

    def quantile(self, q: float) -> Any:
        """Return the exact q-quantile, or None for an empty window."""
        if (
            not isinstance(q, (int, float))
            or isinstance(q, bool)
            or math.isnan(q)
            or q < 0
            or q > 1
        ):
            raise ValueError("q must be between 0 and 1")
        if self._active_count == 0:
            return None
        if q == 1:
            index = self._active_count - 1
        else:
            index = math.floor(q * (self._active_count - 1))
        return _kth(self._root, index)

    def active_values_sorted(self) -> list[Any]:
        """Return a materialized sorted view, useful for tests and inspection."""
        return _values_in_order(self._root)

    def memory_summary(self) -> dict[str, int]:
        """Report retained identities, index nodes, and heap entry counts."""
        return {
            "unique_event_records": len(self._events),
            "active_event_count": self._active_count,
            "active_distinct_values": self._distinct_count,
            "waiting_event_count": self._waiting_count,
            "expired_event_count": self._expired_count,
            "retracted_event_count": self._retracted_count,
            "active_heap_entries": len(self._active_heap),
            "waiting_heap_entries": len(self._waiting_heap),
            "treap_nodes": self._distinct_count,
        }
