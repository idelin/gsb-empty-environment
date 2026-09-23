"""Downstream applier: applies revisions along the version DAG.

States are kept strictly separate:
  - waiting-gap: a base version has not arrived yet -> pause, do NOT
    apply out of order; retried automatically once the gap fills.
  - conflict: the base version is known but stale (a newer version of the
    same key is already applied) -> rejected, so an old correction can
    never overwrite a newer result.
Re-delivering an already applied revision id is a duplicate no-op.
"""

from __future__ import annotations

from .model import Revision

APPLIED = "applied"
DUPLICATE = "duplicate"
WAITING_GAP = "waiting-gap"
CONFLICT = "conflict"


class DownstreamApplier:
    def __init__(self):
        self.values = {}
        self.heads = {}          # agg_key -> revision id
        self.applied = {}        # revision id -> Revision
        self.pending = {}        # revision id -> Revision (waiting for a gap)
        self.conflicts = []      # (revision_id, key, stale_base, current_head)
        self.duplicates = []     # revision ids delivered more than once
        self._conflicted_ids = set()

    def receive(self, rev: Revision) -> str:
        if rev.id in self.applied or rev.id in self.pending \
                or rev.id in self._conflicted_ids:
            self.duplicates.append(rev.id)
            return DUPLICATE
        status = self._try_apply(rev)
        if status == APPLIED:
            self._drain_pending()
        return status

    def _try_apply(self, rev: Revision) -> str:
        for key, base in rev.bases.items():
            current = self.heads.get(key)
            if base is None:
                if key in self.heads:
                    return self._conflict(rev, key, base, current)
                continue
            if base not in self.applied:
                self.pending[rev.id] = rev
                return WAITING_GAP
            if current != base:
                return self._conflict(rev, key, base, current)
        for key, delta in rev.deltas.items():
            self.values[key] = self.values.get(key, 0) + delta
            self.heads[key] = rev.id
        self.applied[rev.id] = rev
        return APPLIED

    def _conflict(self, rev: Revision, key, base, current) -> str:
        self.conflicts.append((rev.id, key, base, current))
        self._conflicted_ids.add(rev.id)
        return CONFLICT

    def _drain_pending(self) -> None:
        progress = True
        while progress:
            progress = False
            for rev_id in list(self.pending):
                rev = self.pending.pop(rev_id)
                status = self._try_apply(rev)
                if status == APPLIED:
                    progress = True

    def acks(self) -> list:
        """Ack evidence for the publisher: one entry per applied revision."""
        return [f"ack-{rid}" for rid in self.applied]
