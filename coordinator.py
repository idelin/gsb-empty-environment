"""Sharded commit coordinator with explicit two-phase (prepare/publish) state.

Commit protocol:
  1. Aggregate the accumulated micro-batch gradients.
  2. PREPARE: every shard purely computes and stages its update. Any
     non-finite gradient (overflow) aborts the whole commit: all staged
     state is discarded, the accumulator is cleared, and neither
     parameters, momentum buffers, nor the step counter change.
  3. PUBLISH: only when every shard is PREPARED does each shard atomically
     swap its staged update in and advance its step count.

A checkpoint captures parameters, momentum, step counts, the parameter
version, every unconfirmed (not yet committed) micro-batch contribution,
and any staged-but-unpublished shard update, so a crash between prepare
and publish can be resolved by recover().
"""

from __future__ import annotations

from enum import Enum

from accumulator import GradientAccumulator
from optimizer import SGD, NonFiniteGradientError
from tensor import Tensor


class CommitStatus(Enum):
    COMMITTED = "committed"
    SKIPPED_NON_FINITE = "skipped_non_finite"
    NOT_READY = "not_ready"


class ShardState(Enum):
    IDLE = "idle"
    PREPARED = "prepared"


class Shard:
    """Owns a partition of the parameters and their optimizer state."""

    def __init__(self, shard_id: str, optimizer: SGD):
        self.shard_id = shard_id
        self.optimizer = optimizer
        self.state = ShardState.IDLE
        self._staged = None

    @property
    def param_names(self) -> set:
        return set(self.optimizer.params)

    def prepare(self, grads: dict) -> None:
        if self.state is not ShardState.IDLE:
            raise RuntimeError(f"shard {self.shard_id!r} is not idle")
        self._staged = self.optimizer.compute_update(grads)
        self.state = ShardState.PREPARED

    def publish(self) -> None:
        if self.state is not ShardState.PREPARED:
            raise RuntimeError(f"shard {self.shard_id!r} has nothing staged")
        self.optimizer.apply_update(*self._staged)
        self._staged = None
        self.state = ShardState.IDLE

    def abort(self) -> None:
        self._staged = None
        self.state = ShardState.IDLE

    def snapshot(self) -> dict:
        return {
            "shard_id": self.shard_id,
            "state": self.state.value,
            "optimizer": self.optimizer.snapshot(),
            "staged": None
            if self._staged is None
            else {
                "params": {n: list(t.data) for n, t in self._staged[0].items()},
                "momentum": {
                    n: list(t.data) for n, t in self._staged[1].items()
                },
            },
        }

    @classmethod
    def from_snapshot(cls, snap: dict) -> "Shard":
        shard = cls(snap["shard_id"], SGD.from_snapshot(snap["optimizer"]))
        shard.state = ShardState(snap["state"])
        if snap["staged"] is not None:
            shard._staged = (
                {n: Tensor(v) for n, v in snap["staged"]["params"].items()},
                {n: Tensor(v) for n, v in snap["staged"]["momentum"].items()},
            )
        return shard


class Coordinator:
    def __init__(self, shards: list, expected_microbatches: int,
                 param_version: int = 0):
        if not shards:
            raise ValueError("at least one shard is required")
        if expected_microbatches < 1:
            raise ValueError("expected_microbatches must be >= 1")
        self.shards = list(shards)
        self.expected_microbatches = expected_microbatches
        self.accumulator = GradientAccumulator(param_version)

    @property
    def param_version(self) -> int:
        return self.accumulator.param_version

    @property
    def step_count(self) -> int:
        return self.shards[0].optimizer.step_count

    def submit(self, batch_id: str, grads: dict, param_version: int) -> bool:
        return self.accumulator.submit(batch_id, grads, param_version)

    def withdraw(self, batch_id: str) -> bool:
        return self.accumulator.withdraw(batch_id)

    def commit(self) -> CommitStatus:
        if len(self.accumulator) < self.expected_microbatches:
            return CommitStatus.NOT_READY
        grads = self.accumulator.aggregate()
        try:
            for shard in self.shards:
                shard_grads = {n: grads[n] for n in shard.param_names}
                shard.prepare(shard_grads)
        except NonFiniteGradientError:
            for shard in self.shards:
                shard.abort()
            self.accumulator.clear()
            return CommitStatus.SKIPPED_NON_FINITE
        for shard in self.shards:
            shard.publish()
        self.accumulator.clear()
        self.accumulator.advance_version()
        return CommitStatus.COMMITTED

    def checkpoint(self) -> dict:
        """JSON-serializable snapshot including unconfirmed batches."""
        return {
            "expected_microbatches": self.expected_microbatches,
            "accumulator": self.accumulator.snapshot(),
            "shards": [s.snapshot() for s in self.shards],
        }

    @classmethod
    def restore(cls, snap: dict) -> "Coordinator":
        coord = cls(
            [Shard.from_snapshot(s) for s in snap["shards"]],
            snap["expected_microbatches"],
        )
        coord.accumulator = GradientAccumulator.from_snapshot(
            snap["accumulator"]
        )
        return coord

    def recover(self) -> None:
        """Resolve an in-flight two-phase commit after a crash.

        If every shard reached PREPARED the commit decision is completed
        (publish); otherwise all staged state is aborted. Either way no
        parameter is left partially updated.
        """
        prepared = [s for s in self.shards if s.state is ShardState.PREPARED]
        if not prepared:
            return
        if len(prepared) == len(self.shards):
            for shard in prepared:
                shard.publish()
            self.accumulator.clear()
            self.accumulator.advance_version()
        else:
            for shard in prepared:
                shard.abort()
