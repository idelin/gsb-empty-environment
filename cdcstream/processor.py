"""Incremental processors under test.

``IncrementalProcessor`` applies delivery items one at a time, maintaining
a materialized state incrementally.  It is idempotent (last-write-wins per
key by commit_seq) and crash-safe (a crash only loses the volatile dedupe
cache; redelivered events are re-applied harmlessly).

``ArrivalOrderProcessor`` is a deliberately buggy variant used to demo the
validator and shrinker: it applies events in arrival order and ignores
commit_seq, so out-of-order delivery corrupts the final state.
"""

from __future__ import annotations

from typing import Any

from .model import ChangeEvent, CrashPoint, DeliveryItem


class IncrementalProcessor:
    """Event-at-a-time materializer (the algorithm under test)."""

    def __init__(self) -> None:
        # Durable materialized state: key -> (commit_seq, row value).
        # A deleted row keeps a tombstone (commit_seq, None) so that a
        # stale redelivery after a crash cannot resurrect it.
        self._state: dict[tuple[str, str], tuple[int, Any]] = {}
        # Volatile dedupe cache, lost on crash.
        self._seen: set[int] = set()

    def apply(self, item: DeliveryItem) -> None:
        if isinstance(item, CrashPoint):
            self._seen.clear()  # volatile state lost; durable state survives
            return
        if not isinstance(item, ChangeEvent):
            raise TypeError(f"unknown delivery item: {item!r}")
        if item.event_id in self._seen:
            return  # exact duplicate delivery
        self._seen.add(item.event_id)
        current = self._state.get(item.key)
        if current is not None and current[0] >= item.commit_seq:
            return  # stale redelivery of an older version
        self._state[item.key] = (item.commit_seq, item.after)

    def run(self, items: list[DeliveryItem]) -> None:
        for item in items:
            self.apply(item)

    def state(self) -> dict[tuple[str, str], Any]:
        return {
            key: value
            for key, (seq, value) in self._state.items()
            if value is not None
        }


class ArrivalOrderProcessor(IncrementalProcessor):
    """Buggy variant: last-write-wins by arrival order, ignores commit_seq."""

    def apply(self, item: DeliveryItem) -> None:
        if isinstance(item, CrashPoint):
            self._seen.clear()
            return
        if item.event_id in self._seen:
            return
        self._seen.add(item.event_id)
        if item.after is None:
            self._state.pop(item.key, None)
        else:
            self._state[item.key] = (item.commit_seq, item.after)
