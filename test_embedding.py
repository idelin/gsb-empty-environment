"""Runnable tests for the sharded embedding + sparse optimizer.

Run with:  python3.11 -m unittest -v
"""

import os
import tempfile
import unittest
from unittest import mock

from embedding import (
    GradientAccumulator,
    ShardedEmbedding,
    SparseMomentumSGD,
    StaleGradientError,
    load_checkpoint,
    save_checkpoint,
)
from minitensor import vec_close, vec_zeros
from reference import DenseReference


def init_fn(row, col):
    # Deterministic, distinct per (row, col).
    return 0.1 * (row + 1) + 0.01 * (col + 1)


def make_table(num_rows=6, dim=3, num_shards=2, frozen=()):
    return ShardedEmbedding(num_rows, dim, num_shards, init_fn,
                            frozen_rows=frozen)


def initial_weights(emb):
    return [[init_fn(r, c) for c in range(emb.dim)]
            for r in range(emb.num_rows)]


class LookupAndMergeTest(unittest.TestCase):
    def test_lookup_returns_copies(self):
        emb = make_table()
        rows = emb.lookup([2, 2, 5])
        self.assertEqual(rows[0], rows[1])
        self.assertEqual(rows[0], [init_fn(2, c) for c in range(3)])
        rows[0][0] = 999.0
        self.assertNotEqual(emb.lookup([2])[0][0], 999.0)

    def test_duplicate_indices_merged_row2_doubled(self):
        # Indices [2, 2, 5]: row 2's gradient contribution doubles.
        emb = make_table()
        acc = GradientAccumulator(emb)
        g = [1.0, 1.0, 1.0]
        g5 = [0.5, 0.5, 0.5]
        acc.accumulate([2, 2, 5], [g, g, g5])
        self.assertEqual(acc.merged[2], [2.0, 2.0, 2.0])
        self.assertEqual(acc.merged[5], g5)

        opt = SparseMomentumSGD(emb, lr=1.0, beta=0.0, weight_decay=0.0)
        self.assertTrue(opt.step(acc, batch_id="dup-1"))
        w2 = emb.lookup([2])[0]
        for c in range(3):
            self.assertAlmostEqual(w2[c], init_fn(2, c) - 2.0)
        w5 = emb.lookup([5])[0]
        for c in range(3):
            self.assertAlmostEqual(w5[c], init_fn(5, c) - 0.5)


class MicroBatchAccumulationTest(unittest.TestCase):
    def test_two_microbatches_merge_and_step_advances_once(self):
        emb = make_table()
        acc = GradientAccumulator(emb)
        acc.accumulate([1, 2], [[1.0, 0.0, 0.0], [0.0, 1.0, 0.0]])
        acc.accumulate([2, 3], [[0.0, 0.5, 0.0], [2.0, 2.0, 2.0]])
        # Duplicate index 2 across micro-batches is summed.
        self.assertEqual(acc.merged[2], [0.0, 1.5, 0.0])

        opt = SparseMomentumSGD(emb, lr=0.1, beta=0.0, weight_decay=0.0)
        self.assertTrue(opt.step(acc, batch_id="mb-1"))
        # Rows 1,2 live in shard 0; row 3 in shard 1: one advance each.
        self.assertEqual(emb.shards[0].step, 1)
        self.assertEqual(emb.shards[1].step, 1)

        # Replaying the same batch id is a no-op (idempotent).
        before = [emb.lookup([r])[0] for r in (1, 2, 3)]
        self.assertFalse(opt.step(acc, batch_id="mb-1"))
        self.assertEqual(emb.shards[0].step, 1)
        self.assertEqual(emb.shards[1].step, 1)
        after = [emb.lookup([r])[0] for r in (1, 2, 3)]
        self.assertEqual(before, after)


class MigrationTest(unittest.TestCase):
    def test_stale_gradient_rejected_after_migration(self):
        emb = make_table()
        self.assertEqual(emb.routing[1], 0)
        acc = GradientAccumulator(emb)  # captures layout version 0
        acc.accumulate([1], [[1.0, 1.0, 1.0]])

        emb.migrate(1, 1)  # row 1 moves shard 0 -> shard 1
        self.assertEqual(emb.layout_version, 1)
        self.assertEqual(emb.routing[1], 1)

        opt = SparseMomentumSGD(emb, lr=1.0, beta=0.0)
        with self.assertRaises(StaleGradientError):
            opt.step(acc, batch_id="stale-1")
        # Nothing was written to the wrong shard.
        self.assertEqual(emb.lookup([1])[0],
                         [init_fn(1, c) for c in range(3)])
        self.assertEqual(emb.shards[0].step, 0)
        self.assertEqual(emb.shards[1].step, 0)
        self.assertNotIn("stale-1", opt.applied_batches)

        # A fresh accumulator under the new layout applies to shard 1.
        acc2 = GradientAccumulator(emb)
        acc2.accumulate([1], [[1.0, 1.0, 1.0]])
        self.assertTrue(opt.step(acc2, batch_id="fresh-1"))
        self.assertEqual(emb.shards[1].step, 1)
        self.assertEqual(emb.shards[0].step, 0)
        w1 = emb.lookup([1])[0]
        for c in range(3):
            self.assertAlmostEqual(w1[c], init_fn(1, c) - 1.0)

    def test_migration_carries_momentum(self):
        emb = make_table()
        opt = SparseMomentumSGD(emb, lr=0.1, beta=0.9)
        acc = GradientAccumulator(emb)
        acc.accumulate([4], [[1.0, 1.0, 1.0]])
        opt.step(acc, batch_id="pre-migrate")
        m_before = list(emb.shard_of(4).momentum[4])
        self.assertTrue(any(v != 0.0 for v in m_before))
        emb.migrate(4, 0)
        self.assertEqual(emb.shard_of(4).momentum[4], m_before)
        self.assertNotIn(4, emb.shards[1].rows)


class AtomicityTest(unittest.TestCase):
    def test_second_shard_failure_rolls_back_first(self):
        emb = make_table()
        opt = SparseMomentumSGD(emb, lr=0.5, beta=0.9, weight_decay=0.01)
        acc = GradientAccumulator(emb)
        acc.accumulate([1, 4], [[1.0, 1.0, 1.0], [2.0, 2.0, 2.0]])

        snap0 = emb.shards[0].snapshot()
        snap1 = emb.shards[1].snapshot()
        with mock.patch.object(
            emb.shards[1], "apply_update",
            side_effect=RuntimeError("simulated shard failure"),
        ):
            with self.assertRaises(RuntimeError):
                opt.step(acc, batch_id="atomic-1")

        # Shard 0 committed first but must be fully rolled back:
        # weights, momentum AND step count.
        self.assertEqual(emb.shards[0].snapshot(), snap0)
        self.assertEqual(emb.shards[1].snapshot(), snap1)
        self.assertEqual(emb.shards[0].step, 0)
        self.assertNotIn("atomic-1", opt.applied_batches)

        # The batch id was not recorded, so the step can be retried.
        self.assertTrue(opt.step(acc, batch_id="atomic-1"))
        self.assertEqual(emb.shards[0].step, 1)
        self.assertEqual(emb.shards[1].step, 1)


class DenseReferenceEquivalenceTest(unittest.TestCase):
    def test_sharded_matches_dense_over_many_steps(self):
        num_rows, dim, num_shards = 8, 4, 3
        lr, beta, wd = 0.05, 0.9, 0.01
        frozen = (5,)
        emb = make_table(num_rows, dim, num_shards, frozen=frozen)
        opt = SparseMomentumSGD(emb, lr=lr, beta=beta, weight_decay=wd)
        ref = DenseReference(initial_weights(emb), lr, beta, wd,
                             frozen_rows=frozen)

        script = [
            [([0, 0, 3], [[0.3] * dim, [0.7] * dim, [1.0] * dim])],
            [([1, 4], [[0.1] * dim, [0.2] * dim]),
             ([4, 6], [[0.4] * dim, [0.6] * dim])],
            [([5, 5, 2], [[1.0] * dim, [1.0] * dim, [0.5] * dim])],
            [([0, 7], [[0.9] * dim, [0.9] * dim]),
             ([7, 1], [[0.8] * dim, [0.8] * dim])],
        ]
        for i, micro_batches in enumerate(script):
            acc = GradientAccumulator(emb)
            for indices, grads in micro_batches:
                acc.accumulate(indices, grads)
            self.assertTrue(opt.step(acc, batch_id=f"eq-{i}"))
            ref.step_batch(micro_batches)

        for row in range(num_rows):
            w = emb.lookup([row])[0]
            self.assertTrue(
                vec_close(w, ref.w[row]),
                f"weight mismatch row {row}: {w} vs {ref.w[row]}",
            )
            m = emb.shard_of(row).momentum[row]
            self.assertTrue(
                vec_close(m, ref.m[row]),
                f"momentum mismatch row {row}",
            )
        # Row 5 is frozen, row 7 was touched; a never-touched row would
        # also be checked here: row 3 was touched in step 0, so use a
        # dedicated untouched row check below instead.
        self.assertEqual(emb.lookup([5])[0],
                         [init_fn(5, c) for c in range(dim)])
        self.assertEqual(emb.shard_of(5).momentum[5], vec_zeros(dim))

    def test_untouched_rows_get_no_weight_decay(self):
        # Explicit, consistent rule: weight decay only applies to rows
        # touched by the current step's merged gradients.
        emb = make_table(num_rows=4, dim=2, num_shards=2)
        opt = SparseMomentumSGD(emb, lr=0.1, beta=0.9, weight_decay=0.5)
        for i in range(3):
            acc = GradientAccumulator(emb)
            acc.accumulate([0], [[1.0, 1.0]])
            opt.step(acc, batch_id=f"wd-{i}")
        # Rows 1..3 never touched: identical to init despite wd=0.5.
        for row in (1, 2, 3):
            self.assertEqual(emb.lookup([row])[0],
                             [init_fn(row, c) for c in range(2)])
            self.assertEqual(emb.shard_of(row).momentum[row],
                             vec_zeros(2))
        # Touched row 0 did decay: its update exceeds the raw gradient.
        self.assertNotEqual(emb.lookup([0])[0],
                            [init_fn(0, c) - 0.1 * 3 for c in range(2)])


class FrozenAndEmptyTest(unittest.TestCase):
    def test_frozen_rows_never_update(self):
        emb = make_table(frozen=(3,))
        opt = SparseMomentumSGD(emb, lr=1.0, beta=0.0, weight_decay=0.1)
        acc = GradientAccumulator(emb)
        acc.accumulate([3, 4], [[1.0, 1.0, 1.0], [1.0, 1.0, 1.0]])
        opt.step(acc, batch_id="frozen-1")
        # Frozen row 3: weight and momentum untouched, no decay.
        self.assertEqual(emb.lookup([3])[0],
                         [init_fn(3, c) for c in range(3)])
        self.assertEqual(emb.shard_of(3).momentum[3], vec_zeros(3))
        # Row 4 updated normally (shard 1 advanced once thanks to it).
        self.assertEqual(emb.shards[1].step, 1)
        self.assertNotEqual(emb.lookup([4])[0],
                            [init_fn(4, c) for c in range(3)])

    def test_empty_indices_step(self):
        emb = make_table()
        opt = SparseMomentumSGD(emb, lr=1.0, beta=0.0)
        acc = GradientAccumulator(emb)
        acc.accumulate([], [])
        self.assertTrue(opt.step(acc, batch_id="empty-1"))
        self.assertEqual([s.step for s in emb.shards], [0, 0])
        for row in range(emb.num_rows):
            self.assertEqual(emb.lookup([row])[0],
                             [init_fn(row, c) for c in range(3)])
        # Idempotent: replaying the empty batch is skipped.
        self.assertFalse(opt.step(acc, batch_id="empty-1"))


class CheckpointRecoveryTest(unittest.TestCase):
    def test_checkpoint_roundtrip_and_idempotent_replay(self):
        emb = make_table(num_rows=6, dim=3, num_shards=2, frozen=(0,))
        opt = SparseMomentumSGD(emb, lr=0.2, beta=0.9, weight_decay=0.01)
        acc = GradientAccumulator(emb)
        acc.accumulate([1, 1, 4], [[0.5] * 3, [0.5] * 3, [1.0] * 3])
        opt.step(acc, batch_id="ckpt-1")
        emb.migrate(4, 0)
        acc2 = GradientAccumulator(emb)
        acc2.accumulate([4, 2], [[0.25] * 3, [0.75] * 3])
        opt.step(acc2, batch_id="ckpt-2")

        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "ckpt.json")
            save_checkpoint(emb, opt, path)
            emb2, opt2 = load_checkpoint(path)

        # Full state recovered: weights, momentum, steps, layout,
        # frozen set and applied batch ids.
        self.assertEqual(emb2.layout_version, emb.layout_version)
        self.assertEqual(emb2.frozen_rows, emb.frozen_rows)
        self.assertEqual(emb2.routing, emb.routing)
        for row in range(emb.num_rows):
            self.assertEqual(emb2.lookup([row])[0], emb.lookup([row])[0])
            self.assertEqual(emb2.shard_of(row).momentum[row],
                             emb.shard_of(row).momentum[row])
        self.assertEqual([s.step for s in emb2.shards],
                         [s.step for s in emb.shards])
        self.assertEqual(opt2.applied_batches, {"ckpt-1", "ckpt-2"})

        # Replaying an already-applied batch id after recovery is a
        # no-op: recovery is idempotent by batch id.
        acc3 = GradientAccumulator(emb2)
        acc3.accumulate([1], [[9.0, 9.0, 9.0]])
        self.assertFalse(opt2.step(acc3, batch_id="ckpt-2"))
        for row in range(emb.num_rows):
            self.assertEqual(emb2.lookup([row])[0], emb.lookup([row])[0])

        # A genuinely new batch id still applies after recovery.
        self.assertTrue(opt2.step(acc3, batch_id="ckpt-3"))
        self.assertNotEqual(emb2.lookup([1])[0], emb.lookup([1])[0])


if __name__ == "__main__":
    unittest.main()
