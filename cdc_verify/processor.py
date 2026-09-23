"""Verify-before-apply processor.

Processing rules (the reference model):

1. Never blind last-write-wins: every event's ``before`` is checked
   against current state before its ``after`` is applied.
2. Partial before-values validate only the fields they provide; fields
   not provided keep their current values (both in validation and in
   the merged ``after``).
3. Multiple events on the same key inside one transaction are validated
   in declared order, each against the result of the previous event in
   the same transaction -- not all against the pre-transaction snapshot.
4. Atomicity boundary: the transaction is the atomic unit. If any event
   conflicts, the whole transaction is rejected and the store is left
   untouched. Independent transactions (disjoint, non-quarantined keys)
   keep flowing; a conflict never halts the stream.
5. Conflict isolation: the conflicting key is quarantined with minimal
   field evidence. Later transactions touching that key are queued
   (wholly, preserving atomicity) until the conflict is resolved.
6. Duplicate commits (same txn_id + table + pk + commit_seq) are
   idempotently skipped. Late old versions (commit_seq lower than the
   last applied seq for the key) and commit_seq reuse by a different
   event are rejected as conflicts.
7. A ``before`` on an absent row reports a missing-row conflict; an
   absent field is not equal to null.
"""

from __future__ import annotations

from collections import defaultdict, deque
from typing import Iterable, Optional

from .model import (
    MISSING,
    ConflictRecord,
    Event,
    FieldConflict,
    Report,
)


class Processor:
    def __init__(self, seed: Optional[Iterable[dict]] = None):
        self.store: dict[tuple[str, str], dict] = {}
        self.last_seq: dict[tuple[str, str], int] = {}
        self.seen: set[tuple] = set()
        self.quarantine: dict[tuple[str, str], ConflictRecord] = {}
        self.pending: dict[tuple[str, str], deque] = defaultdict(deque)
        self.report = Report()
        for row in seed or []:
            self.store[(row["table"], row["pk"])] = dict(row["row"])

    def row(self, table: str, pk: str) -> Optional[dict]:
        current = self.store.get((table, pk))
        return None if current is None else dict(current)

    def process_transaction(self, events: Iterable[Event]) -> str:
        """Apply one transaction atomically; return its status."""
        events = list(events)
        if not events:
            raise ValueError("transaction must contain at least one event")
        txn_id = events[0].txn_id

        fresh = []
        local = set()
        for event in events:
            if event.fingerprint in self.seen or event.fingerprint in local:
                self.report.duplicates.append(event.fingerprint)
                continue
            local.add(event.fingerprint)
            fresh.append(event)
        if not fresh:
            return "duplicate"

        for event in fresh:
            if event.key in self.quarantine:
                self.pending[event.key].append(fresh)
                self.report.blocked.append(
                    {
                        "txn_id": txn_id,
                        "waiting_on": list(event.key),
                        "reason": self.quarantine[event.key].kind,
                    }
                )
                return "blocked"

        shadow = dict(self.store)
        for event in fresh:
            conflict = self._validate(event, shadow)
            if conflict is not None:
                self.quarantine[event.key] = conflict
                self.report.conflicts.append(conflict)
                return "rejected"
            self._apply(event, shadow)

        self.store = shadow
        for event in fresh:
            self.last_seq[event.key] = event.commit_seq
            self.seen.add(event.fingerprint)
        self.report.applied.append(txn_id)
        return "applied"

    def resolve(self, table: str, pk: str, strategy: str) -> str:
        """Resolve a quarantined key and drain its waiting transactions.

        ``accept_current``: rebase each waiting event's ``before`` onto
        the current row (fields absent now are dropped from the
        precondition) and replay the queued transactions in order.
        ``discard``: drop the queued transactions for the key.
        """
        key = (table, pk)
        if key not in self.quarantine:
            return "not_quarantined"
        record = self.quarantine.pop(key)

        if strategy == "discard":
            dropped = [txn[0].txn_id for txn in self.pending.pop(key, deque())]
            self.report.resolutions.append(
                {
                    "key": list(key),
                    "strategy": strategy,
                    "conflict": record.kind,
                    "dropped_txns": dropped,
                }
            )
            return "discarded"

        if strategy == "accept_current":
            queue = self.pending.get(key, deque())
            replayed, failed = [], None
            while queue:
                txn = queue[0]
                rebased = [self._rebase(event, key) for event in txn]
                status = self.process_transaction(rebased)
                if status in ("applied", "duplicate"):
                    replayed.append(txn[0].txn_id)
                    queue.popleft()
                    continue
                failed = txn[0].txn_id
                break
            if not queue:
                self.pending.pop(key, None)
            self.report.resolutions.append(
                {
                    "key": list(key),
                    "strategy": strategy,
                    "conflict": record.kind,
                    "replayed_txns": replayed,
                    "failed_txn": failed,
                }
            )
            return "replayed" if failed is None else "reconflicted"

        raise ValueError(f"unknown strategy: {strategy}")

    def _validate(self, event: Event, shadow: dict) -> Optional[ConflictRecord]:
        last = self.last_seq.get(event.key)
        if last is not None and event.commit_seq < last:
            return ConflictRecord(
                event.txn_id, event.table, event.pk, event.commit_seq,
                "late_version",
                detail=f"commit_seq {event.commit_seq} < last applied {last}",
            )
        if last is not None and event.commit_seq == last:
            return ConflictRecord(
                event.txn_id, event.table, event.pk, event.commit_seq,
                "seq_reuse",
                detail=f"commit_seq {event.commit_seq} already used by another event",
            )

        row = shadow.get(event.key)

        if event.before is None:
            if row is not None:
                return ConflictRecord(
                    event.txn_id, event.table, event.pk, event.commit_seq,
                    "insert_exists",
                    detail="insert expects an absent row",
                )
            if event.after is None:
                return ConflictRecord(
                    event.txn_id, event.table, event.pk, event.commit_seq,
                    "delete_missing",
                    detail="delete expects an existing row",
                )
            return None

        if row is None:
            return ConflictRecord(
                event.txn_id, event.table, event.pk, event.commit_seq,
                "missing_row",
                fields=[
                    FieldConflict(name, value, MISSING)
                    for name, value in event.before.items()
                ],
                detail="before provided but the row is absent (missing event?)",
            )

        mismatched = [
            FieldConflict(name, value, row.get(name, MISSING))
            for name, value in event.before.items()
            if name not in row or row[name] != value
        ]
        if mismatched:
            return ConflictRecord(
                event.txn_id, event.table, event.pk, event.commit_seq,
                "before_mismatch",
                fields=mismatched,
            )
        return None

    def _apply(self, event: Event, shadow: dict) -> None:
        if event.after is None:
            shadow.pop(event.key, None)
            return
        row = dict(shadow.get(event.key) or {})
        row.update(event.after)
        shadow[event.key] = row

    def _rebase(self, event: Event, key: tuple[str, str]) -> Event:
        if event.key != key or event.before is None:
            return event
        row = self.store.get(key) or {}
        before = {name: row[name] for name in event.before if name in row}
        return Event(
            event.txn_id, event.table, event.pk,
            event.commit_seq, before, event.after,
        )
