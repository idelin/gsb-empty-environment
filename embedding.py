"""Row-sharded embedding table with a sparse momentum optimizer.

Pure standard library; small CPU tensors are plain ``list[float]``.

Explicit semantics (kept consistent everywhere, including the dense
reference in ``reference.py``):

Weight decay
    Applied ONLY to rows touched by the current step's merged
    gradients (sparse semantics). Untouched rows receive no decay and
    no momentum update.
Frozen rows
    Lookups keep working; their gradients are dropped by the
    optimizer, so neither weight nor momentum ever changes and they
    never receive weight decay.
Step count
    Per shard; advances exactly once per optimizer step in which the
    shard updates at least one row. An empty step advances nothing but
    still records its batch id.
Atomicity
    A step snapshots every shard it is about to touch; if any shard
    fails mid-commit, all shards are restored. A failed step never
    leaves a partially updated (weight, momentum, step) triple.
Idempotency
    Applied batch ids are recorded and persisted in checkpoints; a
    replayed batch id is skipped.
Migration
    Moving a row between shards bumps ``layout_version``. Gradients
    are tagged with the layout version they were produced under; a
    stale gradient is rejected (``StaleGradientError``) instead of
    being written to the wrong shard.

Update rule (per touched row, per step)::

    g' = g + weight_decay * w
    m  = beta * m + g'
    w  = w - lr * m
"""

import json

from minitensor import vec_add_into, vec_copy, vec_zeros


class StaleGradientError(RuntimeError):
    """Gradient was produced under an older layout version."""


class Shard:
    """Owns an arbitrary set of rows: weights, momentum, step count."""

    def __init__(self, shard_id, dim):
        self.shard_id = shard_id
        self.dim = dim
        self.rows = {}      # row_id -> weight vector
        self.momentum = {}  # row_id -> momentum vector
        self.step = 0

    def apply_update(self, items, lr, beta, weight_decay):
        """Apply merged gradients; advances the shard step once."""
        for row_id, grad in items:
            w = self.rows[row_id]
            m = self.momentum[row_id]
            for j in range(self.dim):
                g = grad[j] + weight_decay * w[j]
                m[j] = beta * m[j] + g
                w[j] -= lr * m[j]
        self.step += 1

    def snapshot(self):
        return (
            {r: vec_copy(w) for r, w in self.rows.items()},
            {r: vec_copy(m) for r, m in self.momentum.items()},
            self.step,
        )

    def restore(self, snap):
        rows, momentum, step = snap
        self.rows = {r: vec_copy(w) for r, w in rows.items()}
        self.momentum = {r: vec_copy(m) for r, m in momentum.items()}
        self.step = step


class ShardedEmbedding:
    """Embedding table whose rows are distributed across shards."""

    def __init__(self, num_rows, dim, num_shards, init_fn, frozen_rows=()):
        if num_shards < 1 or num_shards > num_rows:
            raise ValueError("bad shard count")
        self.num_rows = num_rows
        self.dim = dim
        self.layout_version = 0
        self.frozen_rows = set(frozen_rows)
        self.shards = [Shard(i, dim) for i in range(num_shards)]
        self.routing = {}  # row_id -> shard_id
        # Contiguous blocks; later rows may move via migrate().
        base, extra = divmod(num_rows, num_shards)
        row = 0
        for sid, shard in enumerate(self.shards):
            count = base + (1 if sid < extra else 0)
            for _ in range(count):
                shard.rows[row] = [float(init_fn(row, c)) for c in range(dim)]
                shard.momentum[row] = vec_zeros(dim)
                self.routing[row] = sid
                row += 1

    def shard_of(self, row_id):
        return self.shards[self.routing[row_id]]

    def lookup(self, indices):
        out = []
        for idx in indices:
            if not 0 <= idx < self.num_rows:
                raise IndexError(f"row {idx} out of range")
            out.append(vec_copy(self.shard_of(idx).rows[idx]))
        return out

    def migrate(self, row_id, target_shard_id):
        """Move one row (weight + momentum) to another shard."""
        src = self.shard_of(row_id)
        dst = self.shards[target_shard_id]
        if src is dst:
            return
        dst.rows[row_id] = src.rows.pop(row_id)
        dst.momentum[row_id] = src.momentum.pop(row_id)
        self.routing[row_id] = target_shard_id
        self.layout_version += 1


class GradientAccumulator:
    """Merges gradients across micro-batches of one optimization step.

    Duplicate indices are summed. The layout version is captured at
    creation so gradients produced before a migration cannot be
    applied afterwards.
    """

    def __init__(self, embedding):
        self.layout_version = embedding.layout_version
        self.dim = embedding.dim
        self.num_rows = embedding.num_rows
        self.merged = {}  # row_id -> summed gradient

    def accumulate(self, indices, grad_outputs):
        if len(indices) != len(grad_outputs):
            raise ValueError("indices/grad_outputs length mismatch")
        for idx, g in zip(indices, grad_outputs):
            if not 0 <= idx < self.num_rows:
                raise IndexError(f"row {idx} out of range")
            if len(g) != self.dim:
                raise ValueError("gradient dimension mismatch")
            slot = self.merged.get(idx)
            if slot is None:
                self.merged[idx] = vec_copy(g)
            else:
                vec_add_into(slot, g)


class SparseMomentumSGD:
    """Sparse momentum SGD with atomic multi-shard steps."""

    def __init__(self, embedding, lr, beta=0.9, weight_decay=0.0):
        self.emb = embedding
        self.lr = lr
        self.beta = beta
        self.weight_decay = weight_decay
        self.applied_batches = set()

    def step(self, accumulator, batch_id):
        """Apply one accumulated step. Returns True if applied.

        Atomic across shards: any failure rolls every touched shard
        back to its pre-step snapshot and the batch id is not
        recorded, so the step can be retried safely.
        """
        if batch_id in self.applied_batches:
            return False
        if accumulator.layout_version != self.emb.layout_version:
            raise StaleGradientError(
                f"gradient layout version {accumulator.layout_version} "
                f"!= current {self.emb.layout_version}"
            )
        plans = {}
        for row_id, grad in accumulator.merged.items():
            if row_id in self.emb.frozen_rows:
                continue  # frozen rows never update
            shard = self.emb.shard_of(row_id)
            plans.setdefault(shard, []).append((row_id, grad))
        snapshots = {id(s): s.snapshot() for s in plans}
        try:
            for shard, items in plans.items():
                shard.apply_update(items, self.lr, self.beta,
                                   self.weight_decay)
        except Exception:
            for shard in plans:
                shard.restore(snapshots[id(shard)])
            raise
        self.applied_batches.add(batch_id)
        return True


def save_checkpoint(embedding, optimizer, path):
    state = {
        "format": 1,
        "num_rows": embedding.num_rows,
        "dim": embedding.dim,
        "layout_version": embedding.layout_version,
        "frozen_rows": sorted(embedding.frozen_rows),
        "routing": {str(r): s for r, s in embedding.routing.items()},
        "shards": [
            {
                "shard_id": sh.shard_id,
                "step": sh.step,
                "rows": {str(r): w for r, w in sh.rows.items()},
                "momentum": {str(r): m for r, m in sh.momentum.items()},
            }
            for sh in embedding.shards
        ],
        "optimizer": {
            "lr": optimizer.lr,
            "beta": optimizer.beta,
            "weight_decay": optimizer.weight_decay,
            "applied_batches": sorted(optimizer.applied_batches),
        },
    }
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(state, fh)


def load_checkpoint(path):
    """Rebuild (embedding, optimizer); applied batch ids survive, so
    recovery is idempotent by batch id."""
    with open(path, "r", encoding="utf-8") as fh:
        state = json.load(fh)
    emb = ShardedEmbedding.__new__(ShardedEmbedding)
    emb.num_rows = state["num_rows"]
    emb.dim = state["dim"]
    emb.layout_version = state["layout_version"]
    emb.frozen_rows = set(state["frozen_rows"])
    emb.routing = {int(r): s for r, s in state["routing"].items()}
    emb.shards = []
    for sh_state in state["shards"]:
        shard = Shard(sh_state["shard_id"], state["dim"])
        shard.step = sh_state["step"]
        shard.rows = {int(r): list(w) for r, w in sh_state["rows"].items()}
        shard.momentum = {
            int(r): list(m) for r, m in sh_state["momentum"].items()
        }
        emb.shards.append(shard)
    opt_state = state["optimizer"]
    opt = SparseMomentumSGD(
        emb,
        lr=opt_state["lr"],
        beta=opt_state["beta"],
        weight_decay=opt_state["weight_decay"],
    )
    opt.applied_batches = set(opt_state["applied_batches"])
    return emb, opt
