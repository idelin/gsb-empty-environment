"""Ingestion pipeline: fixture -> validated batch -> atomic commit."""
from __future__ import annotations

import json
from pathlib import Path
from typing import List

from .models import ChangeEvent, OutboxRecord
from .store import StateStore


def load_fixture(path: str) -> List[ChangeEvent]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    events = [ChangeEvent.from_dict(item) for item in raw]
    events.sort(key=lambda e: e.commit_seq)
    return events


class Pipeline:
    def __init__(self, store: StateStore, source: str = "fixture"):
        self._store = store
        self._source = source

    @property
    def offset(self) -> int:
        return self._store.get_offset(self._source)

    def ingest(self, events: List[ChangeEvent]) -> int:
        """Commit one batch atomically. Returns the new offset.

        Only events beyond the confirmed offset are considered. The outbox
        writes and the offset advance commit together; on failure nothing is
        persisted, so the batch stays retryable as-is.
        """
        current = self.offset
        fresh = [e for e in events if e.commit_seq > current]
        if not fresh:
            return current
        # Validate the whole batch BEFORE touching the store: a rejected
        # batch leaves the offset untouched and can be fixed and retried.
        for event in fresh:
            if event.before is None and event.after is None:
                raise ValueError(
                    f"invalid event {event.tx_id}: no before/after values")
        records = [
            OutboxRecord(
                idempotency_id=e.idempotency_id,
                payload=e.to_output_payload(),
                status="pending",
                commit_seq=e.commit_seq,
            )
            for e in fresh
        ]
        new_offset = max(e.commit_seq for e in fresh)
        self._store.commit_batch(self._source, records, new_offset)
        return new_offset
