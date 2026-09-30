"""Hand-computed numerical verification of the accumulate/commit math.

Expected values below are derived independently (plain arithmetic in the
test itself), not by running the system under test.
"""

import math
import unittest

from coordinator import CommitStatus, Coordinator, Shard
from optimizer import SGD
from tensor import Tensor

LR = 0.1
MU = 0.9


def make_coordinator(expected=2):
    shard = Shard("only", SGD({"w": Tensor([1.0, -2.0])}, LR, MU))
    return Coordinator([shard], expected)


class NumericalTest(unittest.TestCase):
    def test_single_commit_hand_computed(self):
        coord = make_coordinator()
        coord.submit("b1", {"w": Tensor([0.5, -0.25])}, 0)
        coord.submit("b2", {"w": Tensor([1.5, 0.75])}, 0)
        self.assertEqual(coord.commit(), CommitStatus.COMMITTED)

        # grad sum = [2.0, 0.5]; momentum = 0.9*0 + sum; w -= 0.1 * momentum
        expected_mom = [0.9 * 0.0 + 2.0, 0.9 * 0.0 + 0.5]
        expected_w = [1.0 - LR * expected_mom[0], -2.0 - LR * expected_mom[1]]
        opt = coord.shards[0].optimizer
        self.assertEqual(opt.momentum_buffers["w"].data, expected_mom)
        self.assertEqual(opt.params["w"].data, expected_w)
        self.assertEqual(opt.step_count, 1)
        self.assertEqual(coord.param_version, 1)

    def test_momentum_carries_across_commits(self):
        coord = make_coordinator()
        coord.submit("b1", {"w": Tensor([0.5, -0.25])}, 0)
        coord.submit("b2", {"w": Tensor([1.5, 0.75])}, 0)
        coord.commit()
        coord.submit("b3", {"w": Tensor([1.0, 1.0])}, 1)
        coord.submit("b4", {"w": Tensor([0.0, -1.0])}, 1)
        self.assertEqual(coord.commit(), CommitStatus.COMMITTED)

        mom1 = [2.0, 0.5]
        grad2 = [1.0, 0.0]
        expected_mom = [MU * mom1[0] + grad2[0], MU * mom1[1] + grad2[1]]
        w1 = [1.0 - LR * mom1[0], -2.0 - LR * mom1[1]]
        expected_w = [w1[0] - LR * expected_mom[0], w1[1] - LR * expected_mom[1]]
        opt = coord.shards[0].optimizer
        self.assertEqual(opt.momentum_buffers["w"].data, expected_mom)
        self.assertEqual(opt.params["w"].data, expected_w)
        self.assertEqual(opt.step_count, 2)

    def test_nonfinite_second_microbatch_keeps_everything(self):
        coord = make_coordinator()
        coord.submit("b1", {"w": Tensor([0.5, -0.25])}, 0)
        coord.submit("b2", {"w": Tensor([math.nan, 1.0])}, 0)
        self.assertEqual(coord.commit(), CommitStatus.SKIPPED_NON_FINITE)
        opt = coord.shards[0].optimizer
        self.assertEqual(opt.params["w"].data, [1.0, -2.0])
        self.assertEqual(opt.momentum_buffers["w"].data, [0.0, 0.0])
        self.assertEqual(opt.step_count, 0)
        self.assertEqual(coord.param_version, 0)
        self.assertEqual(len(coord.accumulator), 0)  # cleared after skip

    def test_infinite_gradient_also_skips(self):
        coord = make_coordinator()
        coord.submit("b1", {"w": Tensor([math.inf, 0.0])}, 0)
        coord.submit("b2", {"w": Tensor([0.5, 0.5])}, 0)
        self.assertEqual(coord.commit(), CommitStatus.SKIPPED_NON_FINITE)
        self.assertEqual(coord.shards[0].optimizer.params["w"].data, [1.0, -2.0])


if __name__ == "__main__":
    unittest.main()
