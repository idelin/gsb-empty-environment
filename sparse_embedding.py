"""Row-sharded sparse embedding table with an atomic sparse optimizer.

Pure Python 3.11, standard library only. No autodiff, no tensor libraries.

Design decisions (explicit and consistent):

* Weight decay: applied ONLY to non-frozen rows touched by the current
  step's merged gradient (sparse semantics). Untouched rows are never
  decayed. Frozen rows are never decayed and never updated.
* Update rule (per touched, non-frozen row):
      g_eff = grad + weight_decay * param
      mom   = beta * mom + g_eff
      param = param - lr * mom
* Empty gradient (or all-touched-rows-frozen) step is a no-op: it does
  not advance the step count and does not consume a batch id.
* Atomicity: one optimizer step updates params, momentum and the step
  count across ALL shards or none. If any shard fails while applying,
  every already-applied shard is rolled back to its pre-step state and
  the step count is not advanced.
* Idempotent recovery: a step carries an optional batch id; a step whose
  batch id equals the last committed batch id is a no-op. Checkpoints
  capture params, momentum, routing, routing versions, step count and
  last batch id, so restore + replay of the same batch id is safe.
* Migration: each row has a routing version, bumped on migration.
  Gradients carry the routing version captured at lookup (forward) time.
  A gradient whose version no longer matches the row's current version
  is stale and is rejected before any shard is written, so a gradient
  produced on an old route can never be written into the wrong shard.
"""

from __future__ import annotations

import random


class ShardApplyError(RuntimeError):
    """Raised when a shard fails to apply its part of an update."""


class StaleGradientError(RuntimeError):
    """Raised when a gradient's routing version is outdated."""


class Router:
    """Maps row id -> shard id, with a per-row routing version."""

    def __init__(self):
        self.row_to_shard = {}
        self.version = {}

    def shard_of(self, row):
        return self.row_to_shard[row]

    def version_of(self, row):
        return self.version[row]


class Shard:
    """Owns a subset of parameter rows and their momentum buffers."""

    def __init__(self, shard_id):
        self.shard_id = shard_id
        self.params = {}    # row -> list[float]
        self.momentum = {}  # row -> list[float]
        self.fail_apply = False  # fault injection for tests

    def add_row(self, row, param, momentum=None):
        self.params[row] = list(param)
        self.momentum[row] = (
            list(momentum) if momentum is not None else [0.0] * len(param)
        )

    def remove_row(self, row):
        param = self.params.pop(row)
        momentum = self.momentum.pop(row)
        return param, momentum

    def backup(self, rows):
        return {
            row: (list(self.params[row]), list(self.momentum[row]))
            for row in rows
        }

    def apply(self, updates):
        """updates: row -> (new_param, new_momentum). Atomic within shard."""
        if self.fail_apply:
            raise ShardApplyError(
                f"shard {self.shard_id} failed to apply update"
            )
        for row, (param, momentum) in updates.items():
            self.params[row] = list(param)
            self.momentum[row] = list(momentum)

    def restore(self, backup):
        """Rollback path; bypasses fault injection."""
        for row, (param, momentum) in backup.items():
            self.params[row] = list(param)
            self.momentum[row] = list(momentum)


class ShardedEmbedding:
    """Embedding table whose rows are distributed over shards."""

    def __init__(self, num_rows, dim, num_shards, seed=0):
        if num_shards < 1:
            raise ValueError("num_shards must be >= 1")
        self.dim = dim
        self.num_rows = num_rows
        self.shards = [Shard(i) for i in range(num_shards)]
        self.router = Router()
        rng = random.Random(seed)
        for row in range(num_rows):
            shard_id = row % num_shards
            self.router.row_to_shard[row] = shard_id
            self.router.version[row] = 0
            init = [rng.uniform(-0.5, 0.5) for _ in range(dim)]
            self.shards[shard_id].add_row(row, init)

    def shard_of(self, row):
        return self.shards[self.router.shard_of(row)]

    def get_row(self, row):
        return list(self.shard_of(row).params[row])

    def set_row(self, row, values):
        if len(values) != self.dim:
            raise ValueError("dimension mismatch")
        shard = self.shard_of(row)
        shard.params[row] = list(values)
        shard.momentum[row] = [0.0] * self.dim

    def get_momentum(self, row):
        return list(self.shard_of(row).momentum[row])

    def forward(self, indices):
        """Lookup. Returns (vectors, versions) where versions maps each
        distinct row to its routing version at lookup time. The caller
        must attach these versions to the gradients it later produces."""
        vectors = [self.get_row(row) for row in indices]
        versions = {row: self.router.version_of(row) for row in set(indices)}
        return vectors, versions

    def migrate_row(self, row, target_shard_id):
        """Move a row (param + momentum) to another shard and bump its
        routing version, invalidating gradients issued on the old route."""
        source_id = self.router.shard_of(row)
        if source_id == target_shard_id:
            return
        source, target = self.shards[source_id], self.shards[target_shard_id]
        param, momentum = source.remove_row(row)
        try:
            target.add_row(row, param, momentum)
        except Exception:
            source.add_row(row, param, momentum)
            raise
        self.router.row_to_shard[row] = target_shard_id
        self.router.version[row] += 1


class GradientAccumulator:
    """Merges gradients across micro-batches within one optimizer step.

    Duplicate indices (within or across micro-batches) are summed, so a
    row referenced k times contributes k times its per-position gradient.
    """

    def __init__(self, dim):
        self.dim = dim
        self.grads = {}     # row -> list[float]
        self.versions = {}  # row -> routing version at backward time

    def backward(self, indices, upstream_grads, versions):
        if len(indices) != len(upstream_grads):
            raise ValueError("indices/upstream length mismatch")
        for pos, row in enumerate(indices):
            grad = upstream_grads[pos]
            if len(grad) != self.dim:
                raise ValueError("gradient dimension mismatch")
            version = versions[row]
            if row in self.versions and self.versions[row] != version:
                raise StaleGradientError(
                    f"row {row}: conflicting routing versions "
                    f"{self.versions[row]} vs {version} within one step"
                )
            self.versions[row] = version
            acc = self.grads.setdefault(row, [0.0] * self.dim)
            for k in range(self.dim):
                acc[k] += grad[k]

    def is_empty(self):
        return not self.grads


class SparseMomentumSGD:
    """Sparse SGD with momentum over a ShardedEmbedding.

    Only rows present in the step's merged gradient are touched.
    """

    def __init__(self, embedding, lr, momentum=0.0, weight_decay=0.0,
                 frozen=()):
        self.embedding = embedding
        self.lr = lr
        self.beta = momentum
        self.weight_decay = weight_decay
        self.frozen = set(frozen)
        self.step_count = 0
        self.last_batch_id = None

    def step(self, accumulator, batch_id=None):
        """Apply one merged gradient step atomically across shards.

        Returns True if an update was committed, False for a no-op
        (duplicate batch id, empty gradient, or only frozen rows).
        """
        if batch_id is not None and batch_id == self.last_batch_id:
            return False  # idempotent replay: already committed
        if accumulator.is_empty():
            return False  # empty gradient: no-op, does not consume batch id

        emb = self.embedding

        # Version validation BEFORE any shard is written: a gradient
        # captured on an old route (e.g. before a migration) is stale
        # and must never reach the wrong shard.
        for row, version in accumulator.versions.items():
            if row not in emb.router.version:
                raise KeyError(f"unknown row {row}")
            if emb.router.version_of(row) != version:
                raise StaleGradientError(
                    f"row {row}: gradient routing version {version} is "
                    f"stale (current {emb.router.version_of(row)}); "
                    f"refusing to write"
                )

        # Phase 1: compute the full update plan in memory.
        plans = {}  # shard -> {row: (new_param, new_momentum)}
        for row, grad in accumulator.grads.items():
            if row in self.frozen:
                continue
            shard = emb.shard_of(row)
            param = shard.params[row]
            mom = shard.momentum[row]
            new_param = [0.0] * emb.dim
            new_mom = [0.0] * emb.dim
            for k in range(emb.dim):
                g_eff = grad[k] + self.weight_decay * param[k]
                m_new = self.beta * mom[k] + g_eff
                new_mom[k] = m_new
                new_param[k] = param[k] - self.lr * m_new
            plans.setdefault(shard, {})[row] = (new_param, new_mom)

        if not plans:
            return False  # only frozen rows touched: no-op

        # Phase 2: apply shard by shard; roll back everything on failure.
        applied = []
        backups = {}
        try:
            for shard, updates in plans.items():
                backups[shard] = shard.backup(updates.keys())
                shard.apply(updates)
                applied.append(shard)
        except ShardApplyError:
            for shard in applied:
                shard.restore(backups[shard])
            raise

        # Step count and batch id advance only after full commit, so
        # params, momentum and step count move atomically as one unit.
        self.step_count += 1
        if batch_id is not None:
            self.last_batch_id = batch_id
        return True

    def checkpoint(self):
        """Snapshot the full trainable state (params, momentum, routing,
        routing versions, step count, last committed batch id)."""
        emb = self.embedding
        return {
            "step_count": self.step_count,
            "last_batch_id": self.last_batch_id,
            "routing": dict(emb.router.row_to_shard),
            "versions": dict(emb.router.version),
            "rows": {
                row: (list(shard.params[row]), list(shard.momentum[row]))
                for shard in emb.shards
                for row in shard.params
            },
        }

    def restore(self, state):
        """Restore from checkpoint(); replays of already-committed batch
        ids remain no-ops afterwards (idempotent recovery)."""
        emb = self.embedding
        for shard in emb.shards:
            shard.params.clear()
            shard.momentum.clear()
        for row, shard_id in state["routing"].items():
            param, momentum = state["rows"][row]
            emb.shards[shard_id].add_row(row, param, momentum)
        emb.router.row_to_shard = dict(state["routing"])
        emb.router.version = dict(state["versions"])
        self.step_count = state["step_count"]
        self.last_batch_id = state["last_batch_id"]
