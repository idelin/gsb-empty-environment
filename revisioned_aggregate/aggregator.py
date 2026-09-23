"""Aggregator: folds change events into versioned correction revisions.

Events are applied strictly in commit_seq order, grouped per transaction.
Each transaction produces at most one revision whose bases are the current
head versions of the touched keys; a revision touching several keys has
several parents, so the version graph is a DAG.
"""

from __future__ import annotations

from collections import defaultdict

from .events import ChangeEvent
from .model import Revision
from .store import RevisionStore


def event_deltas(ev: ChangeEvent, group_field: str) -> dict:
    """Signed contribution of one event to the aggregate, per key."""
    out = defaultdict(int)
    if ev.before is not None:
        out[f"{ev.table}:{ev.before[group_field]}"] -= ev.before["amount"]
    if ev.after is not None:
        out[f"{ev.table}:{ev.after[group_field]}"] += ev.after["amount"]
    return dict(out)


class Aggregator:
    def __init__(self, store: RevisionStore, group_field: str = "account"):
        self.store = store
        self.group_field = group_field
        self.heads, self.values = self._rebuild()

    def _rebuild(self):
        heads = {}
        values = defaultdict(int)
        for rev in self.store.revisions():
            for key, delta in rev.deltas.items():
                values[key] += delta
                heads[key] = rev.id
        return heads, dict(values)

    def ingest(self, events: list) -> list:
        """Fold events (any order) into revisions; returns newly published ones.

        Re-ingesting the same transaction is idempotent: the derived
        revision id already exists, so nothing is published twice.
        """
        by_txn = defaultdict(list)
        for ev in events:
            by_txn[ev.txn_id].append(ev)
        published = []
        for txn_id in sorted(by_txn,
                             key=lambda t: min(e.commit_seq for e in by_txn[t])):
            group = sorted(by_txn[txn_id], key=lambda e: e.commit_seq)
            deltas = defaultdict(int)
            for ev in group:
                for key, delta in event_deltas(ev, self.group_field).items():
                    deltas[key] += delta
            deltas = {k: d for k, d in deltas.items() if d != 0}
            seq_hi = max(e.commit_seq for e in group)
            if not deltas:
                self.store.advance_checkpoint(seq_hi)
                continue
            rev = Revision(
                id=f"rev-{txn_id}",
                txn_id=txn_id,
                bases={k: self.heads.get(k) for k in deltas},
                deltas=dict(deltas),
                seq_lo=min(e.commit_seq for e in group),
                seq_hi=seq_hi,
            )
            if self.store.publish(rev, checkpoint_seq=seq_hi):
                for key, delta in deltas.items():
                    self.values[key] = self.values.get(key, 0) + delta
                    self.heads[key] = rev.id
                published.append(rev)
        return published
