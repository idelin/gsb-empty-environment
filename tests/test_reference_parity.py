"""Parity between the sharded accumulating system and the independent
per-batch reference under randomized fault schedules."""

import random
import unittest

from coordinator import Coordinator, Shard
from faults import FaultSchedule, run_group
from optimizer import SGD
from reference import run_reference
from tensor import Tensor

LR = 0.05
MU = 0.8
EXPECTED = 4
INIT = {"w": [0.3, -1.2, 2.5], "b": [0.7], "g": [-0.4, 0.9]}


def make_coordinator():
    shards = [
        Shard("s0", SGD({"w": Tensor(INIT["w"])}, LR, MU)),
        Shard("s1", SGD({"b": Tensor(INIT["b"])}, LR, MU)),
        Shard("s2", SGD({"g": Tensor(INIT["g"])}, LR, MU)),
    ]
    return Coordinator(shards, EXPECTED)


def random_scenario(seed, groups=6):
    rng = random.Random(seed)
    all_groups = []
    schedules = []
    for g in range(groups):
        n = EXPECTED + rng.randint(0, 2)  # spare batches allow withdrawals
        group = []
        for i in range(n):
            grads = {
                name: Tensor([rng.uniform(-1, 1) for _ in values])
                for name, values in INIT.items()
            }
            group.append((f"g{g}-b{i}", grads))
        ids = [b for b, _ in group]
        nonfinite = set(rng.sample(ids, k=rng.randint(0, 1)))
        withdrawn = set(rng.sample(ids, k=rng.randint(0, n - EXPECTED)))
        duplicates = set(rng.sample(ids, k=rng.randint(0, 2)))
        schedules.append(
            FaultSchedule(
                nonfinite_batches=frozenset(nonfinite),
                duplicate_batches=frozenset(duplicates),
                withdrawn_batches=frozenset(withdrawn),
                shuffle_seed=rng.randint(0, 10**6),
            )
        )
        all_groups.append(group)
    return all_groups, schedules


class MergedFaults:
    def __init__(self, schedules):
        self.nonfinite_batches = frozenset().union(
            *(s.nonfinite_batches for s in schedules)
        )
        self.duplicate_batches = frozenset()
        self.withdrawn_batches = frozenset().union(
            *(s.withdrawn_batches for s in schedules)
        )


class ReferenceParityTest(unittest.TestCase):
    def check_parity(self, seed):
        groups, schedules = random_scenario(seed)
        coord = make_coordinator()
        for group, schedule in zip(groups, schedules):
            run_group(coord, group, schedule)
        ref_params, ref_mom, ref_steps = run_reference(
            INIT,
            [[(b, {n: t.data for n, t in g.items()}) for b, g in group]
             for group in groups],
            LR,
            MU,
            MergedFaults(schedules),
        )
        got_params = {}
        got_mom = {}
        for shard in coord.shards:
            for name, tensor in shard.optimizer.params.items():
                got_params[name] = tensor.data
            for name, tensor in shard.optimizer.momentum_buffers.items():
                got_mom[name] = tensor.data
        self.assertEqual(coord.step_count, ref_steps)
        for name in INIT:
            for got, want in zip(got_params[name], ref_params[name]):
                self.assertAlmostEqual(got, want, places=12)
            for got, want in zip(got_mom[name], ref_mom[name]):
                self.assertAlmostEqual(got, want, places=12)

    def test_parity_seeds(self):
        for seed in (1, 7, 42, 1234, 98765):
            with self.subTest(seed=seed):
                self.check_parity(seed)


if __name__ == "__main__":
    unittest.main()
