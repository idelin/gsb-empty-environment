"""Bounded buffer for changes whose schema is not yet known."""

from __future__ import annotations

from collections import deque
from typing import Any


class BoundedBuffer:
    """FIFO buffer with a hard capacity; oldest entries are evicted first."""

    def __init__(self, capacity: int) -> None:
        if capacity <= 0:
            raise ValueError("capacity must be positive")
        self.capacity = capacity
        self._items: deque[Any] = deque()
        self.evicted = 0

    def add(self, item: Any) -> None:
        while len(self._items) >= self.capacity:
            self._items.popleft()
            self.evicted += 1
        self._items.append(item)

    def take_all(self) -> list[Any]:
        items = list(self._items)
        self._items.clear()
        return items

    def __len__(self) -> int:
        return len(self._items)
