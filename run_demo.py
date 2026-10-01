#!/usr/bin/env python3.11
"""Demo: gradient accumulation under a controllable fault schedule.

Runs the shared scenario (out-of-order micro-batches, retry, revoke,
non-finite overflow skip, stale rejection, duplicate commit, crash between
prepare and publish with checkpoint recovery), then cross-checks the final
parameters, momentum buffers and counters against the independent per-batch
reference implementation.
"""

import json

from gradacc import checkpoint as ckpt
from gradacc.faults import run_schedule
from gradacc.model import loss_and_grad
from gradacc.reference import run_reference
from gradacc.trainer import Trainer
from tests.scenario import (BATCHES, EVENTS, LR, MOMENTUM, make_dataset,
                            make_init_params)


def fmt(params):
    return {k: [round(v, 6) for v in t.data] for k, t in params.items()}


def main():
    dataset = make_dataset()
    init_params = make_init_params()
    print("=== gradient-accumulation demo (stdlib only, Python 3.11) ===")
    print(f"lr={LR} momentum={MOMENTUM} samples={len(dataset)}")
    print(f"init params: {fmt(init_params)}")

    trainer = Trainer({k: v.copy() for k, v in init_params.items()},
                      LR, MOMENTUM)
    trainer, log = run_schedule(trainer, EVENTS, BATCHES, dataset)
    print("\n--- event log ---")
    for entry in log:
        print("  " + " ".join(str(x) for x in entry))

    print("\n--- final trainer state ---")
    print(f"step={trainer.step} version={trainer.version}")
    print(f"params:   {fmt(trainer.params)}")
    print(f"momentum: {fmt(trainer.velocity)}")
    print(f"batches:  {trainer.batch_statuses()}")

    ref = run_reference(init_params, dataset, BATCHES, EVENTS, LR, MOMENTUM)
    print("\n--- parity check vs independent reference ---")
    max_diff = max(
        max(trainer.params[k].max_abs_diff(ref["params"][k]),
            trainer.velocity[k].max_abs_diff(ref["velocity"][k]))
        for k in ref["params"])
    checks = [
        ("step count", trainer.step == ref["step"]),
        ("param version", trainer.version == ref["version"]),
        ("batch statuses", trainer.batch_statuses() == ref["closed"]),
        ("params+momentum (max abs diff %.2e)" % max_diff, max_diff < 1e-9),
    ]
    ok = True
    for name, passed in checks:
        print(f"  [{'OK' if passed else 'FAIL'}] {name}")
        ok = ok and passed

    print("\n--- checkpoint with an unconfirmed batch ---")
    t2 = Trainer({k: v.copy() for k, v in init_params.items()}, LR, MOMENTUM)
    t2.begin_batch("pending-batch")
    _, grads = loss_and_grad(t2.params, [dataset[0]])
    t2.add_microbatch("pending-batch", "m1", grads, weight=1)
    blob = ckpt.dumps(t2)
    payload = json.loads(blob)
    print(f"  open (unconfirmed) batches in checkpoint: "
          f"{sorted(payload['open_batches'])}")
    restored = ckpt.loads(blob)
    result = restored.commit_batch("pending-batch")
    print(f"  restored and committed: {result.status.value}, "
          f"step={restored.step}")

    print("\n=== %s ===" % ("ALL CHECKS PASSED" if ok else "CHECKS FAILED"))
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
