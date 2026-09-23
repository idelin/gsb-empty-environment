"""Dispatcher: deliver pending outbox records to the sink with ack.

Delivery protocol per record:
  1. sink.apply(...)            -> business effect, deduped by idempotency_id
  2. store.mark_acked(...)      -> record acknowledged

Crash between 1 and 2: on restart the record is still 'pending' and gets
re-sent; the sink dedupes it, so no duplicate business effect. Unacked
records are therefore always safe to redeliver.
"""
from __future__ import annotations

from typing import Callable, Optional

from .sink import IdempotentSink
from .store import StateStore


class Dispatcher:
    def __init__(self, store: StateStore, sink: IdempotentSink,
                 after_send_hook: Optional[Callable[[str], None]] = None):
        self._store = store
        self._sink = sink
        # Test/demo hook invoked after sink.apply but before ack (crash point).
        self._after_send_hook = after_send_hook

    def deliver_pending(self) -> int:
        """Deliver all pending records. Returns number of acked records."""
        acked = 0
        for record in self._store.pending_outputs():
            self._sink.apply(record.idempotency_id, record.payload)
            if self._after_send_hook is not None:
                self._after_send_hook(record.idempotency_id)
            self._store.mark_acked(record.idempotency_id)
            acked += 1
        return acked
