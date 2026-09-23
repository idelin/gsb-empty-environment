"""Transaction buffer: buffers changes per transaction and applies them
atomically once the commit marker arrives and the change list is complete.

Guarantees:
  * changes of one transaction are buffered and applied atomically on commit
  * a rollback marker discards the buffered changes
  * a gap in the global sequence pauses consumption until the gap fills
  * a finished transaction (committed or rolled back) never takes effect twice
  * an early commit marker waits for the complete change list
  * changes inside a transaction apply in sequence order (delete-then-insert
    of the same key behaves as expected)
  * a half transaction is never visible to readers, even after a restart
"""
from __future__ import annotations

from typing import Any, Optional

from .events import CHANGE, COMMIT, ROLLBACK, Event, parse_event
from .store import Store

ACCEPTED = "accepted"
DUPLICATE = "duplicate"
FINISHED_TX = "ignored_finished_tx"


class TransactionBuffer:
    def __init__(self, store: Store):
        self._store = store

    @classmethod
    def open(cls, path: str = ":memory:") -> "TransactionBuffer":
        return cls(Store(path))

    def close(self) -> None:
        self._store.close()

    # -- ingestion ---------------------------------------------------------
    def ingest(self, raw: dict) -> str:
        """Accept one raw event (JSON-decoded dict) and drain what is ready."""
        event = parse_event(raw)
        if event.seq <= self._store.get_last_seq():
            return DUPLICATE
        if self._store.tx_outcome(event.tx_id) is not None:
            # Tombstone the seq slot: a finished transaction never takes
            # effect again, but its redelivered events must not leave a
            # permanent gap in the sequence.
            self._store.insert_wal(event, consumed=True)
            self._drain()
            return FINISHED_TX
        if not self._store.insert_wal(event):
            return DUPLICATE
        self._drain()
        return ACCEPTED

    # -- ordered consumption -------------------------------------------------
    def _drain(self) -> None:
        store = self._store
        while True:
            last = store.get_last_seq()
            row = store.wal_row(last + 1)
            if row is None:
                return  # idle or paused on a sequence gap
            event, consumed = row
            if consumed or event.kind == CHANGE:
                # Changes stay buffered until their transaction finishes;
                # consumed rows keep their seq slot so late events cannot
                # silently jump over a gap.
                store.advance_last_seq(last + 1)
                continue
            if event.kind == ROLLBACK:
                store.apply_rollback(event)
                continue
            # COMMIT: wait until the complete change list is buffered.
            assert event.expected_changes is not None
            if store.unconsumed_change_count(event.tx_id) < event.expected_changes:
                return
            changes = store.fetch_tx_changes(event.tx_id)
            store.apply_commit(event, changes)

    # -- committed reads ------------------------------------------------------
    def read(self, table: str, pk: str) -> Optional[Any]:
        return self._store.read(table, pk)

    def read_all(self, table: str) -> dict[str, Any]:
        return self._store.read_all(table)

    # -- introspection ---------------------------------------------------------
    def status(self) -> dict:
        store = self._store
        last = store.get_last_seq()
        state = "idle"
        reason = ""
        row = store.wal_row(last + 1)
        if row is None:
            if store.has_pending_beyond(last):
                state, reason = "paused", f"sequence gap after seq {last}"
        else:
            event, consumed = row
            if not consumed and event.kind == COMMIT:
                have = store.unconsumed_change_count(event.tx_id)
                state = "waiting"
                reason = (
                    f"commit of {event.tx_id} waits for changes "
                    f"({have}/{event.expected_changes})"
                )
        return {
            "state": state,
            "reason": reason,
            "last_seq": last,
            "buffered_changes": store.buffered_change_count(),
        }
