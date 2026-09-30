"""End-to-end demo: sharded gradient accumulation under fault injection,
cross-checked against the independent per-batch reference.

Run with:  python3.11 demo.py
"""

from __future__ import annotations

import json

from coordinator import CommitStatus, Coordinator, Shard
from faults import FaultSchedule, run_group
from optimizer import SGD
from reference import run_reference
from tensor import Tensor

LR = 0.1
MU = 0.9
EXPECTED = 3

INIT_PARAMS = {"w": [1.0, -2.0], "b": [0.5]}


def make_coordinator() -> Coordinator:
    shard0 = Shard("shard-0", SGD({"w": Tensor(INIT_PARAMS["w"])}, LR, MU))
    shard1 = Shard("shard-1", SGD({"b": Tensor(INIT_PARAMS["b"])}, LR, MU))
    return Coordinator([shard0, shard1], EXPECTED)


def tensors(grads: dict) -> dict:
    return {n: Tensor(v) for n, v in grads.items()}


def main() -> None:
    coord = make_coordinator()

    g1 = [
        ("g1-b1", tensors({"w": [0.5, -0.25], "b": [0.10]})),
        ("g1-b2", tensors({"w": [1.5, 0.75], "b": [-0.20]})),  # -> NaN/inf
        ("g1-b3", tensors({"w": [0.25, 0.50], "b": [0.05]})),
    ]
    f1 = FaultSchedule(nonfinite_batches=frozenset({"g1-b2"}))
    s1 = run_group(coord, g1, f1)
    print(f"group 1 (2nd micro-batch non-finite)  -> {s1.value}")
    print(f"  params unchanged: w={coord.shards[0].optimizer.params['w'].data}, "
          f"b={coord.shards[1].optimizer.params['b'].data}, "
          f"steps={coord.step_count}")

    g2 = [
        ("g2-b4", tensors({"w": [1.0, 0.5], "b": [0.30]})),
        ("g2-b5", tensors({"w": [-0.5, 1.5], "b": [0.00]})),
        ("g2-b6", tensors({"w": [9.0, 9.0], "b": [9.00]})),  # withdrawn
        ("g2-b7", tensors({"w": [0.5, -1.0], "b": [0.10]})),
    ]
    f2 = FaultSchedule(
        duplicate_batches=frozenset({"g2-b4"}),
        withdrawn_batches=frozenset({"g2-b6"}),
        shuffle_seed=7,
    )
    s2 = run_group(coord, g2, f2)
    print(f"group 2 (reordered, b4 duplicated, b6 withdrawn) -> {s2.value}")

    g3 = [
        ("g3-b8", tensors({"w": [0.2, 0.1], "b": [0.01]})),
        ("g3-b9", tensors({"w": [0.0, -0.3], "b": [0.02]})),
        ("g3-b10", tensors({"w": [-0.1, 0.4], "b": [-0.03]})),
    ]
    f3 = FaultSchedule()
    version = coord.param_version
    for batch_id, grads in g3:
        coord.submit(batch_id, grads, version)
    snap = json.dumps(coord.checkpoint())  # crash-proof snapshot
    print(f"group 3: checkpointed with unconfirmed batches "
          f"{sorted(c['batch_id'] for c in json.loads(snap)['accumulator']['contributions'])}")
    coord = Coordinator.restore(json.loads(snap))
    s3 = coord.commit()
    print(f"group 3 (restored from checkpoint, then commit) -> {s3.value}")

    g4 = [
        ("g4-b11", tensors({"w": [0.3, 0.0], "b": [0.05]})),
        ("g4-b12", tensors({"w": [0.1, 0.2], "b": [0.00]})),
        ("g4-b13", tensors({"w": [-0.2, 0.1], "b": [0.05]})),
    ]
    version = coord.param_version
    for batch_id, grads in g4:
        coord.submit(batch_id, grads, version)
    agg = coord.accumulator.aggregate()
    for shard in coord.shards:  # PREPARE everywhere, then "crash"
        shard.prepare({n: agg[n] for n in shard.param_names})
    snap = json.dumps(coord.checkpoint())
    coord = Coordinator.restore(json.loads(snap))
    coord.recover()  # all shards PREPARED -> publish
    print(f"group 4 (crash after prepare, recovered via checkpoint) "
          f"-> steps={coord.step_count}")

    ref_params, ref_mom, ref_steps = run_reference(
        INIT_PARAMS,
        [[(b, {n: t.data for n, t in g.items()}) for b, g in grp]
         for grp in (g1, g2, g3, g4)],
        LR,
        MU,
        _MergedFaults(f1, f2),
    )

    got_params = {}
    got_mom = {}
    for shard in coord.shards:
        got_params.update({n: t.data for n, t in shard.optimizer.params.items()})
        got_mom.update(
            {n: t.data for n, t in shard.optimizer.momentum_buffers.items()}
        )

    print()
    print(f"system   : params={got_params}")
    print(f"reference: params={ref_params}")
    print(f"system   : momentum={got_mom}")
    print(f"reference: momentum={ref_mom}")
    print(f"steps: system={coord.step_count} reference={ref_steps}")

    assert coord.step_count == ref_steps, "step count mismatch"
    for name in ref_params:
        for got, want in zip(got_params[name], ref_params[name]):
            assert abs(got - want) < 1e-12, f"param {name} mismatch"
        for got, want in zip(got_mom[name], ref_mom[name]):
            assert abs(got - want) < 1e-12, f"momentum {name} mismatch"
    print("PARITY OK: final params, momentum and step counts match the "
          "independent reference")


class _MergedFaults:
    """Union of the per-group schedules, as seen by the reference."""

    def __init__(self, *schedules: FaultSchedule):
        self.nonfinite_batches = frozenset().union(
            *(s.nonfinite_batches for s in schedules)
        )
        self.duplicate_batches = frozenset().union(
            *(s.duplicate_batches for s in schedules)
        )
        self.withdrawn_batches = frozenset().union(
            *(s.withdrawn_batches for s in schedules)
        )


if __name__ == "__main__":
    main()
