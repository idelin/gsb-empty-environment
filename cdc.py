"""Change data capture primitives fed by local fixtures (no message platform).

A Change carries: transaction id, table, primary key, before/after values,
and a monotonically increasing commit sequence number (the watermark unit).
"""

from __future__ import annotations

import copy
import threading
from dataclasses import dataclass
from typing import Any, Callable, Iterable, Optional


@dataclass(frozen=True)
class Change:
    txn_id: str
    table: str
    pk: Any
    before: Optional[dict]
    after: Optional[dict]  # None means DELETE
    commit_seq: int


@dataclass(frozen=True)
class Snapshot:
    """A fixed, consistent snapshot of the source tables at a given LSN."""

    lsn: int
    tables: dict[str, dict[Any, dict]]


class SnapshotSource:
    """Provides fixed snapshots. Local fixture stand-in for a DB snapshot."""

    def __init__(self, snapshot: Snapshot):
        self._snapshot = snapshot

    def take(self) -> Snapshot:
        return Snapshot(lsn=self._snapshot.lsn, tables=copy.deepcopy(self._snapshot.tables))


class ChangeLog:
    """Append-only, commit_seq-ordered local change log (the fixture source)."""

    def __init__(self, changes: Iterable[Change] = ()):
        self._lock = threading.Lock()
        self._changes: list[Change] = []
        # Test/demo hook: fired on every tip() read; may append new changes
        # to simulate increments arriving while a rebuild is in flight.
        self.on_tip_read: Optional[Callable[[], None]] = None
        for change in changes:
            self.append(change)

    def append(self, change: Change) -> None:
        with self._lock:
            if self._changes and change.commit_seq <= self._changes[-1].commit_seq:
                raise ValueError("commit_seq must be strictly increasing")
            self._changes.append(change)

    def tip(self) -> int:
        """Current high watermark (highest committed commit_seq)."""
        if self.on_tip_read is not None:
            self.on_tip_read()
        with self._lock:
            return self._changes[-1].commit_seq if self._changes else 0

    def read_since(self, watermark: int) -> list[Change]:
        """Buffered increments with commit_seq > watermark, in commit order."""
        with self._lock:
            return [c for c in self._changes if c.commit_seq > watermark]
