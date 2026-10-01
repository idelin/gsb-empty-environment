import unittest

from gradacc.accumulator import BatchStatus, GradientAccumulator
from gradacc.tensor import Tensor

SHAPES = {"w": (1, 2), "b": (1,)}


def grads(w, b):
    return {"w": Tensor(list(w), (1, 2)), "b": Tensor([b], (1,))}


class Recorder:
    def __init__(self):
        self.calls = []

    def __call__(self, g):
        self.calls.append(g)


class AccumulatorTest(unittest.TestCase):
    def setUp(self):
        self.acc = GradientAccumulator(SHAPES)
        self.rec = Recorder()

    def commit(self, batch_id):
        return self.acc.commit_batch(batch_id, self.rec)

    def test_out_of_order_retry_revoke_aggregation(self):
        self.acc.begin_batch("B1")
        # out of order delivery
        self.acc.add_microbatch("B1", "m2", grads((2.0, 2.0), 2.0), weight=1)
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0), weight=3)
        # retry replaces the previous copy of m2
        self.acc.add_microbatch("B1", "m2", grads((4.0, 4.0), 4.0), weight=1)
        # revoked micro must not contribute
        self.acc.add_microbatch("B1", "m9", grads((100.0, 100.0), 100.0))
        self.assertTrue(self.acc.revoke_microbatch("B1", "m9"))
        self.assertFalse(self.acc.revoke_microbatch("B1", "m9"))
        result = self.commit("B1")
        self.assertEqual(result.status, BatchStatus.COMMITTED)
        self.assertTrue(result.applied)
        # weighted mean: (3*[1,1,1] + 1*[4,4,4]) / 4
        (applied,) = self.rec.calls
        self.assertEqual(applied["w"].data, [1.75, 1.75])
        self.assertEqual(applied["b"].data, [1.75])
        self.assertEqual(self.acc.version, 1)

    def test_commit_is_idempotent(self):
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        first = self.commit("B1")
        second = self.commit("B1")
        self.assertIs(first, second)
        self.assertEqual(len(self.rec.calls), 1)
        self.assertEqual(self.acc.version, 1)

    def test_overflow_skips_whole_batch(self):
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        self.acc.add_microbatch("B1", "m2",
                                {"w": Tensor([float("inf"), 0.0], (1, 2)),
                                 "b": Tensor([0.0], (1,))})
        result = self.commit("B1")
        self.assertEqual(result.status, BatchStatus.SKIPPED_OVERFLOW)
        self.assertFalse(result.applied)
        self.assertEqual(self.rec.calls, [])  # apply never invoked
        self.assertEqual(self.acc.version, 0)  # version must not advance
        # re-commit replays the stored skip, still no apply
        self.assertIs(self.commit("B1"), result)
        self.assertEqual(self.rec.calls, [])

    def test_nan_detected_as_overflow(self):
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1",
                                {"w": Tensor([float("nan"), 0.0], (1, 2)),
                                 "b": Tensor([0.0], (1,))})
        self.assertEqual(self.commit("B1").status, BatchStatus.SKIPPED_OVERFLOW)

    def test_stale_batch_rejected(self):
        self.acc.begin_batch("B1")
        self.acc.begin_batch("B2")  # both bound to version 0
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        self.acc.add_microbatch("B2", "m1", grads((2.0, 2.0), 2.0))
        self.assertEqual(self.commit("B1").status, BatchStatus.COMMITTED)
        result = self.commit("B2")
        self.assertEqual(result.status, BatchStatus.REJECTED_STALE)
        self.assertFalse(result.applied)
        self.assertEqual(len(self.rec.calls), 1)  # B2 never applied

    def test_zero_batch_clears_and_rebaselines(self):
        self.acc.begin_batch("B1")
        self.acc.begin_batch("B2")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        self.acc.add_microbatch("B2", "m1", grads((9.0, 9.0), 9.0))
        self.commit("B1")  # version -> 1, B2 now stale
        self.acc.zero_batch("B2")  # clear + re-baseline to version 1
        self.acc.add_microbatch("B2", "m2", grads((2.0, 2.0), 2.0))
        result = self.commit("B2")
        self.assertEqual(result.status, BatchStatus.COMMITTED)
        (g1, g2) = self.rec.calls
        self.assertEqual(g2["w"].data, [2.0, 2.0])  # only m2, m1 was cleared

    def test_empty_commit_rejected(self):
        self.acc.begin_batch("B1")
        result = self.commit("B1")
        self.assertEqual(result.status, BatchStatus.REJECTED_EMPTY)
        self.assertEqual(self.acc.version, 0)

    def test_abort_batch(self):
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        result = self.acc.abort_batch("B1")
        self.assertEqual(result.status, BatchStatus.ABORTED)
        self.assertIs(self.commit("B1"), result)  # closed: idempotent replay
        self.assertEqual(self.rec.calls, [])

    def test_batches_do_not_share_gradients(self):
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        self.commit("B1")
        self.acc.begin_batch("B2")
        self.acc.add_microbatch("B2", "m1", grads((5.0, 5.0), 5.0))
        self.commit("B2")
        (g1, g2) = self.rec.calls
        self.assertEqual(g1["w"].data, [1.0, 1.0])
        self.assertEqual(g2["w"].data, [5.0, 5.0])

    def test_add_after_close_raises(self):
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        self.commit("B1")
        with self.assertRaises(KeyError):
            self.acc.add_microbatch("B1", "m2", grads((1.0, 1.0), 1.0))
        with self.assertRaises(ValueError):
            self.acc.begin_batch("B1")

    def test_unknown_batch_raises(self):
        with self.assertRaises(KeyError):
            self.acc.add_microbatch("nope", "m1", grads((1.0, 1.0), 1.0))
        with self.assertRaises(KeyError):
            self.commit("nope")

    def test_failed_apply_leaves_batch_open(self):
        def boom(_grads):
            raise RuntimeError("simulated crash")
        self.acc.begin_batch("B1")
        self.acc.add_microbatch("B1", "m1", grads((1.0, 1.0), 1.0))
        with self.assertRaises(RuntimeError):
            self.acc.commit_batch("B1", boom)
        self.assertTrue(self.acc.is_open("B1"))
        self.assertEqual(self.acc.version, 0)
        result = self.commit("B1")  # retry succeeds
        self.assertEqual(result.status, BatchStatus.COMMITTED)


if __name__ == "__main__":
    unittest.main()
