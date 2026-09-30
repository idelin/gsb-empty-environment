"""Commit protocol: atomicity across shards, overflow skip, readiness."""

import math
import unittest

from accumulator import StaleVersionError
from coordinator import CommitStatus, Coordinator, Shard, ShardState
from optimizer import SGD
from tensor import Tensor

LR = 0.1
MU = 0.9


def make_two_shard_coordinator(expected=2):
    shard0 = Shard("shard-0", SGD({"w": Tensor([1.0, -2.0])}, LR, MU))
    shard1 = Shard("shard-1", SGD({"b": Tensor([0.5])}, LR, MU))
    return Coordinator([shard0, shard1], expected)


def full_grads(w, b):
    return {"w": Tensor(w), "b": Tensor(b)}


class CommitTest(unittest.TestCase):
    def test_not_ready_until_all_microbatches_arrive(self):
        coord = make_two_shard_coordinator(expected=2)
        coord.submit("b1", full_grads([0.5, 0.5], [0.1]), 0)
        self.assertEqual(coord.commit(), CommitStatus.NOT_READY)
        self.assertEqual(coord.step_count, 0)
        self.assertEqual(len(coord.accumulator), 1)

    def test_commit_is_atomic_across_shards_on_overflow(self):
        coord = make_two_shard_coordinator()
        coord.submit("b1", full_grads([0.5, 0.5], [0.1]), 0)
        coord.submit("b2", full_grads([0.5, 0.5], [math.nan]), 0)
        status = coord.commit()
        self.assertEqual(status, CommitStatus.SKIPPED_NON_FINITE)
        for shard in coord.shards:
            self.assertIs(shard.state, ShardState.IDLE)
            self.assertIsNone(shard._staged)
            self.assertEqual(shard.optimizer.step_count, 0)
        self.assertEqual(
            coord.shards[0].optimizer.params["w"].data, [1.0, -2.0]
        )
        self.assertEqual(coord.shards[1].optimizer.params["b"].data, [0.5])
        self.assertEqual(
            coord.shards[0].optimizer.momentum_buffers["w"].data, [0.0, 0.0]
        )
        self.assertEqual(
            coord.shards[1].optimizer.momentum_buffers["b"].data, [0.0]
        )
        self.assertEqual(coord.param_version, 0)
        self.assertEqual(len(coord.accumulator), 0)

    def test_skip_does_not_pollute_next_commit(self):
        coord = make_two_shard_coordinator()
        coord.submit("b1", full_grads([1.0, 1.0], [1.0]), 0)
        coord.submit("b2", full_grads([math.inf, 0.0], [0.0]), 0)
        self.assertEqual(coord.commit(), CommitStatus.SKIPPED_NON_FINITE)
        coord.submit("b3", full_grads([0.5, -0.25], [0.1]), 0)
        coord.submit("b4", full_grads([1.5, 0.75], [-0.2]), 0)
        self.assertEqual(coord.commit(), CommitStatus.COMMITTED)
        opt0 = coord.shards[0].optimizer
        opt1 = coord.shards[1].optimizer
        self.assertEqual(opt0.momentum_buffers["w"].data, [2.0, 0.5])
        self.assertEqual(opt1.momentum_buffers["b"].data, [-0.1])
        self.assertEqual(opt0.step_count, 1)
        self.assertEqual(opt1.step_count, 1)

    def test_stale_retry_after_commit_rejected(self):
        coord = make_two_shard_coordinator()
        grads = full_grads([0.5, 0.5], [0.1])
        coord.submit("b1", grads, 0)
        coord.submit("b2", full_grads([0.5, 0.5], [0.1]), 0)
        self.assertEqual(coord.commit(), CommitStatus.COMMITTED)
        with self.assertRaises(StaleVersionError):
            coord.submit("b1", grads, 0)
        self.assertEqual(len(coord.accumulator), 0)

    def test_commit_clears_accumulator_and_bumps_version(self):
        coord = make_two_shard_coordinator()
        coord.submit("b1", full_grads([0.5, 0.5], [0.1]), 0)
        coord.submit("b2", full_grads([0.5, 0.5], [0.1]), 0)
        coord.commit()
        self.assertEqual(len(coord.accumulator), 0)
        self.assertEqual(coord.param_version, 1)
        self.assertEqual(coord.step_count, 1)


if __name__ == "__main__":
    unittest.main()
