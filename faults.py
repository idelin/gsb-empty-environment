"""Deterministic fault-injection schedules for tests and the demo.

A FaultSchedule describes, per commit group of micro-batches:
  * nonfinite_batches: batch ids whose gradients are replaced by NaN/inf
    (simulates gradient overflow / loss-scaler trips),
  * duplicate_batches: batch ids delivered twice (idempotent retry),
  * withdrawn_batches: batch ids revoked before commit,
  * shuffle_seed:      if set, deterministically shuffles submission order
    (out-of-order micro-batch arrival).

run_group applies the schedule to a Coordinator and commits; the same
schedule is handed to the independent reference implementation so both
sides observe identical logical faults.
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass

from tensor import Tensor


@dataclass(frozen=True)
class FaultSchedule:
    nonfinite_batches: frozenset = frozenset()
    duplicate_batches: frozenset = frozenset()
    withdrawn_batches: frozenset = frozenset()
    shuffle_seed: int | None = None


def taint(grads: dict) -> dict:
    """Return a copy of grads with every tensor poisoned by NaN/inf."""
    poisoned = {}
    for name, tensor in grads.items():
        data = list(tensor.data)
        if data:
            data[0] = math.nan
        if len(data) > 1:
            data[-1] = math.inf
        poisoned[name] = Tensor(data)
    return poisoned


def run_group(coordinator, batches, faults: FaultSchedule):
    """Submit one micro-batch group under the fault schedule, then commit.

    batches: list of (batch_id, grads) where grads is dict[str, Tensor].
    Returns the CommitStatus of the group commit.
    """
    order = list(batches)
    if faults.shuffle_seed is not None:
        random.Random(faults.shuffle_seed).shuffle(order)
    version = coordinator.param_version
    for batch_id, grads in order:
        if batch_id in faults.nonfinite_batches:
            effective = taint(grads)
        else:
            effective = grads
        coordinator.submit(batch_id, effective, version)
        if batch_id in faults.duplicate_batches:
            coordinator.submit(batch_id, effective, version)
    for batch_id in faults.withdrawn_batches:
        coordinator.withdraw(batch_id)
    return coordinator.commit()
