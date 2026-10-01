"""Parameter shards with an explicit prepare/publish two-phase protocol.

Each shard owns a subset of the parameters plus their momentum buffers.
A batch commit goes through two explicit states:

* PREPARED: ``prepare`` records the decision and gradients; nothing mutates.
* PUBLISHED: ``publish`` applies the decision atomically (all parameters of
  the shard at once, or none). A SKIP decision changes nothing and never
  advances the step counter or touches momentum.

A prepared-but-unpublished batch survives in ``pending`` and is part of the
checkpoint, so a crash between the two phases can be resumed deterministically.
"""

from __future__ import annotations

from enum import Enum

from .tensor import Tensor


class Decision(str, Enum):
    PUBLISH = "publish"
    SKIP = "skip"


class Shard:
    def __init__(self, name, params, lr, momentum):
        self.name = name
        self.params = dict(params)
        self.lr = float(lr)
        self.momentum = float(momentum)
        self.velocity = {k: Tensor.zeros(t.shape)
                         for k, t in self.params.items()}
        self.step = 0
        self.pending = {}    # batch_id -> {"decision": Decision, "grads": {...}}
        self.completed = {}  # batch_id -> "published" | "skipped" | "aborted"

    # -- two-phase commit ----------------------------------------------------
    def prepare(self, batch_id, grads, decision):
        """Phase 1: validate and stash gradients. No state mutation besides
        recording the pending entry. Idempotent."""
        if batch_id in self.completed or batch_id in self.pending:
            return
        for name, grad in grads.items():
            if name not in self.params:
                raise KeyError(f"shard {self.name}: unknown param {name!r}")
            if grad.shape != self.params[name].shape:
                raise ValueError(
                    f"shard {self.name}: shape mismatch for {name!r}")
        self.pending[batch_id] = {"decision": Decision(decision),
                                  "grads": dict(grads)}

    def publish(self, batch_id):
        """Phase 2: apply the prepared decision atomically. Idempotent."""
        if batch_id in self.completed:
            return
        record = self.pending.pop(batch_id)
        if record["decision"] is Decision.PUBLISH:
            # compute everything first, then swap: all-or-nothing
            new_velocity = {}
            new_params = {}
            for name, param in self.params.items():
                grad = record["grads"][name]
                vel = self.velocity[name]
                nv = Tensor([self.momentum * v + g
                             for v, g in zip(vel.data, grad.data)], vel.shape)
                np_ = Tensor([p - self.lr * v
                              for p, v in zip(param.data, nv.data)], param.shape)
                new_velocity[name] = nv
                new_params[name] = np_
            self.velocity = new_velocity
            self.params = new_params
            self.step += 1
            self.completed[batch_id] = "published"
        else:
            # SKIP: parameters, momentum and step counter stay untouched
            self.completed[batch_id] = "skipped"

    def abort(self, batch_id):
        if batch_id in self.completed:
            return
        self.pending.pop(batch_id, None)
        self.completed[batch_id] = "aborted"

    def resume_pending(self):
        """Replay prepared-but-unpublished batches after a crash."""
        for batch_id in sorted(self.pending):
            self.publish(batch_id)
