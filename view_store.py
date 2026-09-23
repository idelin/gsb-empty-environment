"""Materialized view and the versioned store with atomic swap semantics."""

from __future__ import annotations

import threading
from typing import Any, Callable, Optional

from cdc import Change


class View:
    """An immutable-after-publish materialized view over the source tables.

    `watermark` is the highest commit_seq included in this view. Readers that
    grabbed an older version keep seeing it; the store never mutates a view
    once a newer version has been published.
    """

    def __init__(self, version: int, watermark: int, tables: dict[str, dict[Any, dict]]):
        self.version = version
        self.watermark = watermark
        self._tables = tables

    def get(self, table: str, pk: Any) -> Optional[dict]:
        row = self._tables.get(table, {}).get(pk)
        return dict(row) if row is not None else None

    def count(self, table: str) -> int:
        return len(self._tables.get(table, {}))

    def apply(self, change: Change) -> bool:
        """Apply one change. Returns False (no-op) if already included.

        The commit_seq guard is the no-double-count invariant: a change at or
        below the view watermark is never applied twice.
        """
        if change.commit_seq <= self.watermark:
            return False
        rows = self._tables.setdefault(change.table, {})
        if change.after is None:
            rows.pop(change.pk, None)
        else:
            rows[change.pk] = dict(change.after)
        self.watermark = change.commit_seq
        return True


class ViewStore:
    """Holds the currently published view; swaps are atomic and versioned."""

    def __init__(self, initial: View):
        self._lock = threading.Lock()
        self._current = initial
        self._next_version = initial.version + 1

    def current(self) -> View:
        """Grab the current version. The returned handle stays valid for the
        reader even after a swap (old readers keep their version)."""
        with self._lock:
            return self._current

    def publish_if_caught_up(self, view: View, log_tip: Callable[[], int]) -> bool:
        """Atomically publish `view` only if it has caught up to the log tip.

        The catch-up check and the swap happen under one lock, so a view that
        is behind the switch watermark is never published.
        """
        with self._lock:
            if view.watermark < log_tip():
                return False
            view.version = self._next_version
            self._next_version += 1
            self._current = view
            return True

    def apply_live(self, change: Change) -> bool:
        """Route a live change to the current view, deduped by commit_seq."""
        with self._lock:
            return self._current.apply(change)
