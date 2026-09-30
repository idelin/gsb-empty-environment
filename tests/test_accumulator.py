"""Accumulator semantics: order, retry, withdraw, versioning, isolation."""

import unittest

from accumulator import (
    ConflictingBatchError,
    GradientAccumulator,
    StaleVersionError,
)
from tensor import Tensor


def grads(*values):
    return {"w": Tensor(values)}


class AccumulatorTest(unittest.TestCase):
    def test_out_of_order_arrival_same_aggregate(self):
        acc = GradientAccumulator()
        acc.submit("b3", grads(3.0), 0)
        acc.submit("b1", grads(1.0), 0)
        acc.submit("b2", grads(2.0), 0)
        self.assertEqual(acc.aggregate()["w"].data, [6.0])

    def test_duplicate_submit_is_idempotent(self):
        acc = GradientAccumulator()
        self.assertTrue(acc.submit("b1", grads(1.0, 2.0), 0))
        self.assertFalse(acc.submit("b1", grads(1.0, 2.0), 0))
        self.assertEqual(len(acc), 1)
        self.assertEqual(acc.aggregate()["w"].data, [1.0, 2.0])

    def test_conflicting_resubmit_rejected(self):
        acc = GradientAccumulator()
        acc.submit("b1", grads(1.0), 0)
        with self.assertRaises(ConflictingBatchError):
            acc.submit("b1", grads(9.9), 0)

    def test_withdraw_removes_contribution(self):
        acc = GradientAccumulator()
        acc.submit("b1", grads(1.0), 0)
        acc.submit("b2", grads(2.0), 0)
        self.assertTrue(acc.withdraw("b1"))
        self.assertFalse(acc.withdraw("b1"))
        self.assertEqual(acc.aggregate()["w"].data, [2.0])

    def test_stale_param_version_rejected(self):
        acc = GradientAccumulator(param_version=1)
        with self.assertRaises(StaleVersionError):
            acc.submit("b1", grads(1.0), 0)
        self.assertEqual(len(acc), 0)

    def test_clear_has_zero_semantics(self):
        acc = GradientAccumulator()
        acc.submit("b1", grads(5.0), 0)
        acc.clear()
        self.assertEqual(len(acc), 0)
        self.assertEqual(acc.aggregate(), {})
        acc.submit("b2", grads(1.0), 0)
        self.assertEqual(acc.aggregate()["w"].data, [1.0])

    def test_batches_do_not_share_gradients(self):
        acc = GradientAccumulator()
        acc.submit("b1", grads(1.0, 2.0), 0)
        acc.submit("b2", grads(10.0, 20.0), 0)
        acc.withdraw("b2")
        self.assertEqual(acc.aggregate()["w"].data, [1.0, 2.0])
        self.assertEqual(acc.batch_ids, {"b1"})

    def test_snapshot_roundtrip(self):
        acc = GradientAccumulator(param_version=3)
        acc.submit("b1", grads(1.5, -2.5), 3)
        clone = GradientAccumulator.from_snapshot(acc.snapshot())
        self.assertEqual(clone.param_version, 3)
        self.assertEqual(clone.aggregate(), acc.aggregate())
        self.assertEqual(clone.batch_ids, {"b1"})


if __name__ == "__main__":
    unittest.main()
