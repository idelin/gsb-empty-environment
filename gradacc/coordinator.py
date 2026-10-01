"""Two-phase commit coordinator across parameter shards.

commit() runs prepare on every shard, then publish on every shard.
The decision (PUBLISH / SKIP) is computed once from gradient finiteness
and recorded in each shard's pending table, so a crash between the two
phases is resumed deterministically from a checkpoint.
"""

from __future__ import annotations

from .shard import Decision


class Coordinator:
    def __init__(self, shards):
        if not shards:
            raise ValueError("at least one shard is required")
        self.shards = list(shards)
        self.results = {}  # batch_id -> "published" | "skipped"

    def commit(self, batch_id, grads, hook=None):
        """Idempotent two-phase commit. ``hook`` runs between prepare and
        publish (fault-injection point)."""
        if batch_id in self.results:
            return self.results[batch_id]
        decision = (Decision.PUBLISH
                    if all(g.isfinite() for g in grads.values())
                    else Decision.SKIP)
        for shard in self.shards:
            shard.prepare(batch_id, self._slice(shard, grads), decision)
        if hook is not None:
            hook()
        for shard in self.shards:
            shard.publish(batch_id)
        self.results[batch_id] = ("published"
                                  if decision is Decision.PUBLISH else "skipped")
        return self.results[batch_id]

    def resume_pending(self):
        for shard in self.shards:
            shard.resume_pending()

    def _slice(self, shard, grads):
        return {name: grads[name] for name in shard.params}
