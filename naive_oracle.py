from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Any


_WAITING = 0
_ACTIVE = 1
_EXPIRED = 2
_RETRACTED = 3


@dataclass(slots=True)
class _Event:
    timestamp: Any
    value: Any
    status: int


class NaiveQuantileWindow:
    """Small, deliberately simple oracle backed by full scans and sorting."""

    def __init__(self, window_size: float | int) -> None:
        if window_size <= 0:
            raise ValueError("window_size must be positive")
        self._window_size = window_size
        self._events: dict[tuple[Any, Any], _Event] = {}
        self._left: Any = None
        self._right: Any = None

    def ingest(self, source: Any, seq: Any, timestamp: Any, value: Any) -> bool:
        key = (source, seq)
        if key in self._events:
            return False
        if self._right is not None and timestamp <= self._left:
            raise ValueError("event timestamp is older than the current watermark")

        is_active = self._right is not None and timestamp <= self._right
        self._events[key] = _Event(
            timestamp,
            value,
            _ACTIVE if is_active else _WAITING,
        )
        return True

    def advance_watermark(self, timestamp: Any) -> None:
        if self._right is not None and timestamp < self._right:
            raise ValueError("watermark cannot move backwards")

        self._right = timestamp
        self._left = timestamp - self._window_size
        for event in self._events.values():
            if event.status == _RETRACTED:
                continue
            if self._left < event.timestamp <= self._right:
                event.status = _ACTIVE
            elif event.timestamp <= self._left:
                event.status = _EXPIRED
            else:
                event.status = _WAITING

    def retract(self, source: Any, seq: Any) -> bool:
        event = self._events.get((source, seq))
        if event is None or event.status == _RETRACTED:
            return False
        event.status = _RETRACTED
        return True

    def quantile(self, q: float) -> Any:
        if (
            not isinstance(q, (int, float))
            or isinstance(q, bool)
            or math.isnan(q)
            or q < 0
            or q > 1
        ):
            raise ValueError("q must be between 0 and 1")
        values = self.active_values_sorted()
        if not values:
            return None
        if q == 1:
            index = len(values) - 1
        else:
            index = math.floor(q * (len(values) - 1))
        return values[index]

    def active_values_sorted(self) -> list[Any]:
        return sorted(
            event.value
            for event in self._events.values()
            if event.status == _ACTIVE
        )
