"""Controllable fault scheduler.

Executes an event schedule against a Trainer. Supported events:

    ("begin", batch_id)
    ("micro", batch_id, micro_id)            deliver micro-batch gradients
    ("corrupt", batch_id, micro_id[, mode])  deliver non-finite gradients
    ("revoke", batch_id, micro_id)           revoke a delivered micro-batch
    ("zero", batch_id)                       clear an open batch
    ("arm_crash",)                           crash the next commit after prepare
    ("commit", batch_id)                     commit (idempotent)

A crash raises SimulatedCrash between prepare and publish; the executor
restores from the checkpoint taken at the crash point, replays prepared
batches and retries the commit idempotently.
"""

from __future__ import annotations

from . import checkpoint as ckpt
from .model import loss_and_grad
from .tensor import Tensor


class SimulatedCrash(Exception):
    """Raised between prepare and publish when a crash is armed."""


def corrupt_grads(grads, mode="inf"):
    bad = float("inf") if mode == "inf" else float("nan")
    out = {}
    for name, grad in grads.items():
        data = list(grad.data)
        data[0] = bad
        out[name] = Tensor(data, grad.shape)
    return out


def run_schedule(trainer, events, batches, dataset):
    """Execute events against ``trainer``; returns (trainer, log).

    The returned trainer may differ from the input one if a crash forced a
    rebuild from a checkpoint.
    """
    log = []
    state = {"crash_armed": False, "checkpoint": None}

    def crash_hook():
        if state["crash_armed"]:
            state["checkpoint"] = ckpt.dumps(trainer)
            raise SimulatedCrash("crash after prepare, before publish")

    for event in events:
        op = event[0]
        if op == "begin":
            trainer.begin_batch(event[1])
            log.append(("begin", event[1]))
        elif op in ("micro", "corrupt"):
            _, bid, mid = event[:3]
            samples = [dataset[i] for i in batches[bid][mid]]
            _, grads = loss_and_grad(trainer.params, samples)
            if op == "corrupt":
                mode = event[3] if len(event) > 3 else "inf"
                grads = corrupt_grads(grads, mode)
            trainer.add_microbatch(bid, mid, grads, weight=len(samples))
            log.append((op, bid, mid))
        elif op == "revoke":
            removed = trainer.revoke_microbatch(event[1], event[2])
            log.append(("revoke", event[1], event[2], removed))
        elif op == "zero":
            trainer.zero_batch(event[1])
            log.append(("zero", event[1]))
        elif op == "arm_crash":
            state["crash_armed"] = True
            log.append(("arm_crash",))
        elif op == "commit":
            bid = event[1]
            try:
                result = trainer.commit_batch(bid, hook=crash_hook)
                log.append(("commit", bid, result.status.value))
            except SimulatedCrash:
                log.append(("crash", bid))
                trainer = ckpt.loads(state["checkpoint"])
                trainer.resume_pending()
                state["crash_armed"] = False
                result = trainer.commit_batch(bid)  # idempotent retry
                log.append(("recover", bid, result.status.value))
        else:
            raise ValueError(f"unknown event op {op!r}")
    return trainer, log
