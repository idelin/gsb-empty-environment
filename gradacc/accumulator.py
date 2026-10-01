"""Micro-batch gradient accumulation keyed by (batch_id, param version).

Semantics
---------
* Micro-batches may arrive out of order, be retried (same micro_id replaces
  the previous copy) and be revoked before commit.
* Each batch is bound to the parameter version at begin_batch(). A commit
  whose version no longer matches is rejected as stale: nothing is applied.
* commit_batch() is atomic and idempotent: a non-finite gradient skips the
  whole batch (no parameter, momentum or step change), and re-committing a
  closed batch replays the stored result without touching anything.
* Zeroing: commit consumes the batch's gradients (success or skip);
  zero_batch() clears an open batch explicitly and re-baselines its version.
"""

from __future__ import annotations

from enum import Enum

from .tensor import Tensor


class BatchStatus(str, Enum):
    COMMITTED = "committed"                  # gradients applied
    SKIPPED_OVERFLOW = "skipped_overflow"    # non-finite grads: whole batch skipped
    REJECTED_STALE = "rejected_stale"        # parameter version moved on
    REJECTED_EMPTY = "rejected_empty"        # nothing accumulated
    ABORTED = "aborted"                      # explicitly discarded


class CommitResult:
    __slots__ = ("batch_id", "status", "applied", "version", "reason")

    def __init__(self, batch_id, status, applied, version, reason=""):
        self.batch_id = batch_id
        self.status = BatchStatus(status)
        self.applied = bool(applied)
        self.version = int(version)  # parameter version after this result
        self.reason = reason

    def to_json(self):
        return {"batch_id": self.batch_id, "status": self.status.value,
                "applied": self.applied, "version": self.version,
                "reason": self.reason}

    @classmethod
    def from_json(cls, payload):
        return cls(payload["batch_id"], payload["status"], payload["applied"],
                   payload["version"], payload.get("reason", ""))

    def __repr__(self):
        return (f"CommitResult(batch_id={self.batch_id!r}, "
                f"status={self.status.value}, applied={self.applied}, "
                f"version={self.version}, reason={self.reason!r})")


class OpenBatch:
    __slots__ = ("batch_id", "version", "micros")

    def __init__(self, batch_id, version):
        self.batch_id = batch_id
        self.version = version
        self.micros = {}  # micro_id -> {"weight": int, "grads": {name: Tensor}}


class GradientAccumulator:
    def __init__(self, param_shapes):
        self.param_shapes = {k: tuple(v) for k, v in param_shapes.items()}
        self.version = 0
        self.open_batches = {}
        self.closed = {}  # batch_id -> CommitResult (idempotency ledger)

    # -- batch lifecycle -----------------------------------------------------
    def begin_batch(self, batch_id):
        if batch_id in self.closed:
            raise ValueError(f"batch {batch_id!r} is already closed")
        if batch_id in self.open_batches:
            raise ValueError(f"batch {batch_id!r} is already open")
        self.open_batches[batch_id] = OpenBatch(batch_id, self.version)

    def add_microbatch(self, batch_id, micro_id, grads, weight=1):
        batch = self._require_open(batch_id)
        if set(grads) != set(self.param_shapes):
            raise ValueError("gradient keys must match parameter names")
        for name, grad in grads.items():
            if grad.shape != self.param_shapes[name]:
                raise ValueError(f"shape mismatch for parameter {name!r}")
        if weight <= 0:
            raise ValueError("weight must be a positive integer")
        # retry / redelivery of the same micro_id replaces the previous copy
        batch.micros[micro_id] = {
            "weight": int(weight),
            "grads": {k: v.copy() for k, v in grads.items()},
        }

    def revoke_microbatch(self, batch_id, micro_id):
        batch = self._require_open(batch_id)
        return batch.micros.pop(micro_id, None) is not None

    def zero_batch(self, batch_id):
        """Clear accumulated micro-batches. The batch stays open and is
        re-baselined to the current parameter version."""
        batch = self._require_open(batch_id)
        batch.micros.clear()
        batch.version = self.version

    def abort_batch(self, batch_id):
        if batch_id in self.closed:
            return self.closed[batch_id]
        self._require_open(batch_id)
        del self.open_batches[batch_id]
        return self._close(batch_id, BatchStatus.ABORTED, applied=False,
                           reason="aborted")

    # -- commit ----------------------------------------------------------------
    def commit_batch(self, batch_id, apply_update):
        """Commit exactly once. ``apply_update(grads)`` performs the atomic
        parameter update; if it raises (e.g. simulated crash) the batch
        stays open and nothing is recorded."""
        if batch_id in self.closed:
            return self.closed[batch_id]  # idempotent replay
        batch = self._require_open(batch_id)
        if batch.version != self.version:
            del self.open_batches[batch_id]
            return self._close(
                batch_id, BatchStatus.REJECTED_STALE, applied=False,
                reason=(f"batch version {batch.version} != "
                        f"current version {self.version}"))
        if not batch.micros:
            del self.open_batches[batch_id]
            return self._close(batch_id, BatchStatus.REJECTED_EMPTY,
                               applied=False, reason="no micro-batches")
        grads = self._aggregate(batch)
        if not all(g.isfinite() for g in grads.values()):
            del self.open_batches[batch_id]
            return self._close(
                batch_id, BatchStatus.SKIPPED_OVERFLOW, applied=False,
                reason="non-finite gradient detected; whole batch skipped")
        apply_update(grads)
        del self.open_batches[batch_id]
        self.version += 1
        return self._close(batch_id, BatchStatus.COMMITTED, applied=True)

    # -- introspection ---------------------------------------------------------
    def is_open(self, batch_id):
        return batch_id in self.open_batches

    def batch_statuses(self):
        return {bid: res.status.value for bid, res in self.closed.items()}

    # -- internals ---------------------------------------------------------------
    def _require_open(self, batch_id):
        if batch_id in self.open_batches:
            return self.open_batches[batch_id]
        if batch_id in self.closed:
            raise KeyError(
                f"batch {batch_id!r} is already closed "
                f"({self.closed[batch_id].status.value})")
        raise KeyError(f"unknown batch {batch_id!r}; call begin_batch first")

    def _aggregate(self, batch):
        total_weight = sum(m["weight"] for m in batch.micros.values())
        out = {name: Tensor.zeros(shape)
               for name, shape in self.param_shapes.items()}
        for micro_id in sorted(batch.micros):
            micro = batch.micros[micro_id]
            factor = micro["weight"] / total_weight
            for name, grad in micro["grads"].items():
                acc = out[name]
                for i, v in enumerate(grad.data):
                    acc.data[i] += factor * v
        return out

    def _close(self, batch_id, status, applied, reason=""):
        result = CommitResult(batch_id, status, applied, self.version, reason)
        self.closed[batch_id] = result
        return result
