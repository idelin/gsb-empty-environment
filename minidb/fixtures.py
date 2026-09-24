"""Local change fixtures: no message platform, just JSON files.

Each event describes one primary-key change and carries:
    txn_id      -- transaction the change belongs to
    table       -- target table
    pk          -- primary-key column name
    before      -- key value before the change
    after       -- key value after the change
    commit_seq  -- monotonic commit sequence number

Events sharing a ``txn_id`` are applied as one atomic transaction, in
``commit_seq`` order.  Re-delivered events are detected and skipped, and
late events that arrive for a key the entity has already moved away from
are isolated by version instead of being applied.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Dict, Hashable, List, Tuple

from .core import ConflictError, Database, UnknownKeyError


@dataclass(frozen=True)
class Event:
    txn_id: str
    table: str
    pk: str
    before: Hashable
    after: Hashable
    commit_seq: int

    @staticmethod
    def from_dict(raw: Dict[str, Any]) -> "Event":
        return Event(
            txn_id=str(raw["txn_id"]),
            table=str(raw["table"]),
            pk=str(raw["pk"]),
            before=raw["before"],
            after=raw["after"],
            commit_seq=int(raw["commit_seq"]),
        )

    @property
    def dedup_key(self) -> Tuple[str, str, str, int]:
        return (self.txn_id, self.table, repr(self.before), self.commit_seq)


@dataclass
class ApplyReport:
    committed: List[str] = field(default_factory=list)
    failed: Dict[str, str] = field(default_factory=dict)
    duplicates: List[Event] = field(default_factory=list)
    stale: List[Event] = field(default_factory=list)


def load_events(path: str) -> List[Event]:
    """Read a JSON fixture file into a list of events."""
    with open(path, "r", encoding="utf-8") as handle:
        raw = json.load(handle)
    return [Event.from_dict(item) for item in raw]


def apply_events(db: Database, events: List[Event]) -> ApplyReport:
    """Apply fixture events to ``db`` and return a per-event report.

    * duplicate events (same txn, table, old key, commit seq) are skipped;
    * late events whose ``before`` key no longer resolves -- or whose
      commit seq is not newer than the entity's last applied seq -- are
      isolated as stale instead of moving anything;
    * remaining events are grouped by ``txn_id`` and committed as atomic
      transactions; a conflict rolls the whole transaction back.
    """
    report = ApplyReport()
    seen: set = set()
    groups: Dict[str, List[Event]] = {}
    order: List[str] = []

    for event in sorted(events, key=lambda e: (e.commit_seq, e.txn_id)):
        if event.dedup_key in seen:
            report.duplicates.append(event)
            continue
        seen.add(event.dedup_key)
        if event.txn_id not in groups:
            groups[event.txn_id] = []
            order.append(event.txn_id)
        groups[event.txn_id].append(event)

    for txn_id in order:
        txn = db.begin(txn_id)
        planned = 0
        for event in groups[txn_id]:
            table = db.tables[event.table]
            entity_id = table.index.get(event.before)
            if entity_id is None:
                # Late event for an old key: the entity already moved.
                report.stale.append(event)
                continue
            if event.commit_seq <= table.commit_seqs.get(entity_id, 0):
                # Version isolation: a newer change already committed.
                report.stale.append(event)
                continue
            txn.change_primary_key(
                event.table, event.before, event.after, event.commit_seq
            )
            planned += 1
        if planned == 0:
            txn.rollback()
            continue
        try:
            txn.commit()
        except (ConflictError, UnknownKeyError) as exc:
            report.failed[txn_id] = str(exc)
        else:
            report.committed.append(txn_id)

    return report
