"""Independent per-batch reference implementation.

This module shares no code with the sharded accumulating system under
test (no Tensor, no accumulator, no two-phase commit). It processes each
commit group micro-batch by micro-batch with plain lists and floats, and
exists purely to cross-check final parameters, momentum buffers, and
step counts.

Semantics mirrored from the specification:
  * one SGD-with-momentum step per commit group, over the sum of the
    active micro-batch gradients,
  * a group containing a non-finite micro-batch is skipped wholesale:
    no step, no parameter change, momentum left untouched,
  * withdrawn batches contribute nothing; duplicates count once
    (the reference simply iterates the unique active batches).
"""

from __future__ import annotations


def run_reference(initial_params: dict, groups: list, lr: float,
                  momentum: float, faults) -> tuple:
    """Run the reference over all commit groups.

    initial_params: dict[str, list[float]]
    groups: list of groups; each group is a list of (batch_id, grads)
            with grads as dict[str, list[float]]
    faults: a FaultSchedule (only its sets are read)

    Returns (params, momentum_buffers, step_count).
    """
    params = {n: [float(x) for x in v] for n, v in initial_params.items()}
    mom = {n: [0.0] * len(v) for n, v in initial_params.items()}
    steps = 0
    for group in groups:
        seen = set()
        active = []
        for batch_id, grads in group:
            if batch_id in faults.withdrawn_batches or batch_id in seen:
                continue
            seen.add(batch_id)
            active.append((batch_id, grads))
        if any(b in faults.nonfinite_batches for b, _ in active):
            continue  # whole group skipped: no step, momentum preserved
        total = {n: [0.0] * len(params[n]) for n in params}
        for _, grads in active:
            for name, values in grads.items():
                acc = total[name]
                for i, x in enumerate(values):
                    acc[i] += x
        for name in params:
            p = params[name]
            m = mom[name]
            t = total[name]
            for i in range(len(p)):
                m[i] = momentum * m[i] + t[i]
                p[i] -= lr * m[i]
        steps += 1
    return params, mom, steps
