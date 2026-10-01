"""JSON checkpoints.

A checkpoint captures the full trainer state, including:
* parameters, momentum buffers, per-shard step counters,
* prepared-but-unpublished shard commits (two-phase pending table),
* unconfirmed (still open) accumulation batches with their micro-gradients,
* the closed-batch ledger so re-commits stay idempotent after a restore.
"""

from __future__ import annotations

import json

from .accumulator import CommitResult, GradientAccumulator, OpenBatch
from .coordinator import Coordinator
from .shard import Decision, Shard
from .tensor import Tensor
from .trainer import Trainer

FORMAT_VERSION = 1


def trainer_to_dict(trainer):
    acc = trainer.accumulator
    return {
        "format": FORMAT_VERSION,
        "lr": trainer.lr,
        "momentum": trainer.momentum,
        "version": acc.version,
        "shards": {s.name: _shard_to_dict(s) for s in trainer.shards},
        "coordinator_results": dict(trainer.coordinator.results),
        "open_batches": {bid: _open_batch_to_dict(b)
                         for bid, b in acc.open_batches.items()},
        "closed_batches": {bid: r.to_json()
                           for bid, r in acc.closed.items()},
    }


def trainer_from_dict(payload):
    if payload["format"] != FORMAT_VERSION:
        raise ValueError(f"unsupported checkpoint format {payload['format']}")
    shard_payloads = payload["shards"]
    layout = [(name, list(sp["params"])) for name, sp in shard_payloads.items()]
    params = {}
    for sp in shard_payloads.values():
        params.update({k: Tensor.from_json(v) for k, v in sp["params"].items()})
    trainer = Trainer(params, payload["lr"], payload["momentum"],
                      shard_layout=layout)
    for shard in trainer.shards:
        sp = shard_payloads[shard.name]
        shard.velocity = {k: Tensor.from_json(v)
                          for k, v in sp["velocity"].items()}
        shard.step = sp["step"]
        shard.pending = {
            bid: {"decision": Decision(rec["decision"]),
                  "grads": {k: Tensor.from_json(v)
                            for k, v in rec["grads"].items()}}
            for bid, rec in sp["pending"].items()
        }
        shard.completed = dict(sp["completed"])
    trainer.coordinator.results = dict(payload["coordinator_results"])
    acc = trainer.accumulator
    acc.version = payload["version"]
    for bid, bp in payload["open_batches"].items():
        batch = OpenBatch(bid, bp["version"])
        batch.micros = {
            mid: {"weight": m["weight"],
                  "grads": {k: Tensor.from_json(v)
                            for k, v in m["grads"].items()}}
            for mid, m in bp["micros"].items()
        }
        acc.open_batches[bid] = batch
    acc.closed = {bid: CommitResult.from_json(r)
                  for bid, r in payload["closed_batches"].items()}
    return trainer


def dumps(trainer):
    return json.dumps(trainer_to_dict(trainer), sort_keys=True)


def loads(text):
    return trainer_from_dict(json.loads(text))


def _shard_to_dict(shard):
    return {
        "params": {k: t.to_json() for k, t in shard.params.items()},
        "velocity": {k: t.to_json() for k, t in shard.velocity.items()},
        "step": shard.step,
        "pending": {
            bid: {"decision": rec["decision"].value,
                  "grads": {k: t.to_json() for k, t in rec["grads"].items()}}
            for bid, rec in shard.pending.items()
        },
        "completed": dict(shard.completed),
    }


def _open_batch_to_dict(batch):
    return {
        "version": batch.version,
        "micros": {
            mid: {"weight": m["weight"],
                  "grads": {k: t.to_json() for k, t in m["grads"].items()}}
            for mid, m in batch.micros.items()
        },
    }
