"""Trainer: wires the gradient accumulator to the shard coordinator."""

from __future__ import annotations

from .accumulator import GradientAccumulator
from .coordinator import Coordinator
from .shard import Shard


def default_layout(param_names, num_shards=2):
    names = list(param_names)
    num_shards = max(1, min(num_shards, len(names)))
    chunks = [[] for _ in range(num_shards)]
    for i, name in enumerate(names):
        chunks[i % num_shards].append(name)
    return [(f"shard-{i}", chunk) for i, chunk in enumerate(chunks) if chunk]


class Trainer:
    def __init__(self, params, lr, momentum, shard_layout=None):
        self.lr = float(lr)
        self.momentum = float(momentum)
        layout = shard_layout or default_layout(params.keys())
        self.shards = [
            Shard(name, {k: params[k] for k in names}, lr, momentum)
            for name, names in layout
        ]
        self.coordinator = Coordinator(self.shards)
        self.accumulator = GradientAccumulator(
            {k: v.shape for k, v in params.items()})

    # -- views -----------------------------------------------------------------
    @property
    def params(self):
        merged = {}
        for shard in self.shards:
            merged.update(shard.params)
        return merged

    @property
    def velocity(self):
        merged = {}
        for shard in self.shards:
            merged.update(shard.velocity)
        return merged

    @property
    def step(self):
        steps = {s.step for s in self.shards}
        if len(steps) != 1:
            raise RuntimeError(f"shard steps diverged: {sorted(steps)}")
        return steps.pop()

    @property
    def version(self):
        return self.accumulator.version

    # -- accumulation API (delegates) -------------------------------------------
    def begin_batch(self, batch_id):
        self.accumulator.begin_batch(batch_id)

    def add_microbatch(self, batch_id, micro_id, grads, weight=1):
        self.accumulator.add_microbatch(batch_id, micro_id, grads, weight)

    def revoke_microbatch(self, batch_id, micro_id):
        return self.accumulator.revoke_microbatch(batch_id, micro_id)

    def zero_batch(self, batch_id):
        self.accumulator.zero_batch(batch_id)

    def abort_batch(self, batch_id):
        return self.accumulator.abort_batch(batch_id)

    def commit_batch(self, batch_id, hook=None):
        return self.accumulator.commit_batch(
            batch_id,
            lambda grads: self.coordinator.commit(batch_id, grads, hook=hook))

    def resume_pending(self):
        self.coordinator.resume_pending()

    def batch_statuses(self):
        return self.accumulator.batch_statuses()
