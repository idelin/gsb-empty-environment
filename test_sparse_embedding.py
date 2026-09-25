"""Runnable unittest suite for sparse_embedding.

Includes an independent dense (non-sharded) reference implementation used
to numerically verify parameters and momentum of the sharded system.
"""

import unittest

from sparse_embedding import (
    GradientAccumulator,
    ShardApplyError,
    ShardedEmbedding,
    SparseMomentumSGD,
    StaleGradientError,
)


class DenseReference:
    """Independent dense reference: one table, same update rule.

    g_eff = grad + wd * param; mom = beta * mom + g_eff; param -= lr * mom
    Weight decay only on touched, non-frozen rows (sparse semantics).
    """

    def __init__(self, params, lr, beta, wd, frozen=()):
        self.params = {r: list(v) for r, v in params.items()}
        dim = len(next(iter(params.values())))
        self.mom = {r: [0.0] * dim for r in params}
        self.lr, self.beta, self.wd = lr, beta, wd
        self.frozen = set(frozen)
        self.steps = 0

    def step(self, merged_grads):
        if not merged_grads:
            return False
        changed = False
        for row, grad in merged_grads.items():
            if row in self.frozen:
                continue
            changed = True
            param, mom = self.params[row], self.mom[row]
            for k in range(len(param)):
                g_eff = grad[k] + self.wd * param[k]
                mom[k] = self.beta * mom[k] + g_eff
                param[k] -= self.lr * mom[k]
        if changed:
            self.steps += 1
        return changed


def merge_microbatches(microbatches):
    """Reference duplicate-index merge: sum per-position grads per row."""
    merged = {}
    for indices, upstream in microbatches:
        for pos, row in enumerate(indices):
            acc = merged.setdefault(row, [0.0] * len(upstream[pos]))
            for k, g in enumerate(upstream[pos]):
                acc[k] += g
    return merged


def make_accumulator(emb, microbatches):
    acc = GradientAccumulator(emb.dim)
    for indices, upstream in microbatches:
        _, versions = emb.forward(indices)
        acc.backward(indices, upstream, versions)
    return acc


class SparseEmbeddingTest(unittest.TestCase):

    def assertRowAlmostEqual(self, actual, expected, places=12):
        self.assertEqual(len(actual), len(expected))
        for a, e in zip(actual, expected):
            self.assertAlmostEqual(a, e, places=places)

    # 1. indices [2, 2, 5]: row 2 contributes twice.
    def test_duplicate_indices_double_contribution(self):
        emb = ShardedEmbedding(num_rows=8, dim=2, num_shards=2, seed=7)
        acc = GradientAccumulator(emb.dim)
        _, versions = emb.forward([2, 2, 5])
        acc.backward([2, 2, 5], [[1.0, 1.0], [1.0, 1.0], [1.0, 1.0]],
                     versions)
        self.assertRowAlmostEqual(acc.grads[2], [2.0, 2.0])
        self.assertRowAlmostEqual(acc.grads[5], [1.0, 1.0])
        self.assertEqual(set(acc.grads), {2, 5})

    # 2. Two micro-batches in one step: duplicates merged, step count +1.
    def test_two_microbatches_single_step(self):
        emb = ShardedEmbedding(num_rows=6, dim=3, num_shards=2, seed=3)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9)
        initial = {r: emb.get_row(r) for r in range(6)}
        mbs = [([1, 2], [[1.0] * 3, [1.0] * 3]),
               ([2, 3], [[1.0] * 3, [1.0] * 3])]
        acc = make_accumulator(emb, mbs)
        self.assertRowAlmostEqual(acc.grads[2], [2.0, 2.0, 2.0])
        self.assertRowAlmostEqual(acc.grads[1], [1.0, 1.0, 1.0])
        self.assertRowAlmostEqual(acc.grads[3], [1.0, 1.0, 1.0])
        self.assertTrue(opt.step(acc, batch_id="mb-step-1"))
        self.assertEqual(opt.step_count, 1)  # advanced exactly once
        ref = DenseReference(initial, lr=0.1, beta=0.9, wd=0.0)
        ref.step(merge_microbatches(mbs))
        for row in (1, 2, 3):
            self.assertRowAlmostEqual(emb.get_row(row), ref.params[row])
            self.assertRowAlmostEqual(emb.get_momentum(row), ref.mom[row])

    # 3. Stale gradient from an old route is rejected during migration.
    def test_stale_gradient_rejected_during_migration(self):
        emb = ShardedEmbedding(num_rows=6, dim=2, num_shards=2, seed=11)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9)
        row = 3
        self.assertEqual(emb.router.shard_of(row), 1)
        before = emb.get_row(row)
        _, versions = emb.forward([row])          # version captured now
        emb.migrate_row(row, 0)                   # route changes
        acc = GradientAccumulator(emb.dim)
        acc.backward([row], [[5.0, 5.0]], versions)  # old-route gradient
        with self.assertRaises(StaleGradientError):
            opt.step(acc, batch_id="stale-1")
        # Nothing written anywhere; row not corrupted in the new shard.
        self.assertRowAlmostEqual(emb.get_row(row), before)
        self.assertRowAlmostEqual(emb.get_momentum(row), [0.0, 0.0])
        self.assertEqual(opt.step_count, 0)
        self.assertIsNone(opt.last_batch_id)

    # 4. Failure on the second shard rolls back the first.
    def test_second_shard_failure_rolls_back_first(self):
        emb = ShardedEmbedding(num_rows=4, dim=2, num_shards=2, seed=5)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9)
        # row 0 -> shard 0, row 1 -> shard 1 (row % num_shards)
        p0_before = emb.get_row(0)
        p1_before = emb.get_row(1)
        emb.shards[1].fail_apply = True
        acc = make_accumulator(emb, [([0, 1], [[1.0, 1.0], [1.0, 1.0]])])
        with self.assertRaises(ShardApplyError):
            opt.step(acc, batch_id="fail-1")
        # Shard 0 fully rolled back: params and momentum untouched.
        self.assertRowAlmostEqual(emb.get_row(0), p0_before)
        self.assertRowAlmostEqual(emb.get_momentum(0), [0.0, 0.0])
        self.assertRowAlmostEqual(emb.get_row(1), p1_before)
        self.assertEqual(opt.step_count, 0)
        self.assertIsNone(opt.last_batch_id)
        # After clearing the fault the same batch commits cleanly.
        emb.shards[1].fail_apply = False
        self.assertTrue(opt.step(acc, batch_id="fail-1"))
        self.assertEqual(opt.step_count, 1)

    # 5. Multi-step dense-reference verification of params and momentum,
    #    with weight decay, frozen rows and micro-batch merging.
    def test_dense_reference_params_and_momentum(self):
        lr, beta, wd, frozen = 0.05, 0.9, 0.01, {4}
        emb = ShardedEmbedding(num_rows=6, dim=3, num_shards=3, seed=42)
        initial = {r: emb.get_row(r) for r in range(6)}
        opt = SparseMomentumSGD(emb, lr=lr, momentum=beta,
                                weight_decay=wd, frozen=frozen)
        ref = DenseReference(initial, lr=lr, beta=beta, wd=wd, frozen=frozen)
        steps = [
            [([0, 0, 4], [[0.3, -0.1, 0.2]] * 3)],
            [([1, 2], [[0.1, 0.0, -0.2], [0.4, 0.4, 0.4]]),
             ([2, 5], [[0.1, 0.1, 0.1], [-0.3, 0.2, 0.0]])],
            [([0, 5, 5], [[0.05] * 3] * 3)],
        ]
        for i, mbs in enumerate(steps):
            acc = make_accumulator(emb, mbs)
            self.assertTrue(opt.step(acc, batch_id=f"batch-{i}"))
            ref.step(merge_microbatches(mbs))
        self.assertEqual(opt.step_count, ref.steps)
        for row in range(6):
            self.assertRowAlmostEqual(emb.get_row(row), ref.params[row])
            self.assertRowAlmostEqual(emb.get_momentum(row), ref.mom[row])

    # 6. Frozen rows: never updated, never decayed, no momentum change.
    def test_frozen_rows_not_updated(self):
        emb = ShardedEmbedding(num_rows=4, dim=2, num_shards=2, seed=9)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9,
                                weight_decay=0.5, frozen={1})
        frozen_before = emb.get_row(1)
        acc = make_accumulator(emb, [([0, 1], [[1.0, 1.0], [1.0, 1.0]])])
        self.assertTrue(opt.step(acc))
        self.assertRowAlmostEqual(emb.get_row(1), frozen_before)
        self.assertRowAlmostEqual(emb.get_momentum(1), [0.0, 0.0])
        # A step touching ONLY frozen rows is a no-op.
        acc2 = make_accumulator(emb, [([1], [[1.0, 1.0]])])
        self.assertFalse(opt.step(acc2))
        self.assertEqual(opt.step_count, 1)

    # 7. Empty indices: no-op, step count untouched.
    def test_empty_indices_noop(self):
        emb = ShardedEmbedding(num_rows=4, dim=2, num_shards=2, seed=1)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9)
        acc = make_accumulator(emb, [([], [])])
        self.assertTrue(acc.is_empty())
        self.assertFalse(opt.step(acc, batch_id="empty-1"))
        self.assertEqual(opt.step_count, 0)
        self.assertIsNone(opt.last_batch_id)

    # 8. Checkpoint restore + idempotent replay by batch id.
    def test_checkpoint_recovery_idempotent(self):
        emb = ShardedEmbedding(num_rows=6, dim=2, num_shards=2, seed=13)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9)
        acc1 = make_accumulator(emb, [([0, 2], [[1.0, 1.0], [0.5, 0.5]])])
        self.assertTrue(opt.step(acc1, batch_id="b1"))
        ckpt = opt.checkpoint()
        acc2 = make_accumulator(emb, [([1, 3], [[0.2, 0.2], [0.4, 0.4]])])
        self.assertTrue(opt.step(acc2, batch_id="b2"))
        after_b2 = {r: emb.get_row(r) for r in range(6)}
        # Crash + recover from the checkpoint taken after b1.
        opt.restore(ckpt)
        self.assertEqual(opt.step_count, 1)
        # Replaying the already-committed batch b1 is a no-op.
        acc1_replay = make_accumulator(emb, [([0, 2], [[9.9, 9.9]] * 2)])
        self.assertFalse(opt.step(acc1_replay, batch_id="b1"))
        self.assertEqual(opt.step_count, 1)
        for row in range(6):
            expected = ckpt["rows"][row][0]
            self.assertRowAlmostEqual(emb.get_row(row), expected)
        # Re-applying b2 after recovery reproduces the exact same state.
        acc2b = make_accumulator(emb, [([1, 3], [[0.2, 0.2], [0.4, 0.4]])])
        self.assertTrue(opt.step(acc2b, batch_id="b2"))
        self.assertEqual(opt.step_count, 2)
        for row in range(6):
            self.assertRowAlmostEqual(emb.get_row(row), after_b2[row])

    # 9. Weight decay: untouched rows are NOT decayed; touched rows are.
    def test_weight_decay_only_touched_rows(self):
        emb = ShardedEmbedding(num_rows=3, dim=2, num_shards=1, seed=21)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.0, weight_decay=0.1)
        row1_before = emb.get_row(1)
        row2_before = emb.get_row(2)
        p0 = emb.get_row(0)
        acc = make_accumulator(emb, [([0], [[1.0, 1.0]])])
        self.assertTrue(opt.step(acc))
        # Hand-computed: g_eff = 1 + 0.1 * p0; p0' = p0 - 0.1 * g_eff.
        expected0 = [p - 0.1 * (1.0 + 0.1 * p) for p in p0]
        self.assertRowAlmostEqual(emb.get_row(0), expected0)
        self.assertRowAlmostEqual(emb.get_row(1), row1_before)
        self.assertRowAlmostEqual(emb.get_row(2), row2_before)

    # 10. Hand-computed two-step momentum check (independent numerics).
    def test_hand_computed_momentum_two_steps(self):
        emb = ShardedEmbedding(num_rows=2, dim=1, num_shards=1, seed=0)
        emb.set_row(0, [1.0])
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.5)
        acc = make_accumulator(emb, [([0], [[2.0]])])
        opt.step(acc)
        # m1 = 0.5*0 + 2 = 2; p1 = 1 - 0.1*2 = 0.8
        self.assertRowAlmostEqual(emb.get_momentum(0), [2.0])
        self.assertRowAlmostEqual(emb.get_row(0), [0.8])
        acc2 = make_accumulator(emb, [([0], [[2.0]])])
        opt.step(acc2)
        # m2 = 0.5*2 + 2 = 3; p2 = 0.8 - 0.1*3 = 0.5
        self.assertRowAlmostEqual(emb.get_momentum(0), [3.0])
        self.assertRowAlmostEqual(emb.get_row(0), [0.5])
        self.assertEqual(opt.step_count, 2)

    # 11. Migration moves param + momentum and bumps the routing version.
    def test_migration_moves_state_and_bumps_version(self):
        emb = ShardedEmbedding(num_rows=4, dim=2, num_shards=2, seed=17)
        opt = SparseMomentumSGD(emb, lr=0.1, momentum=0.9)
        acc = make_accumulator(emb, [([1], [[1.0, 1.0]])])
        opt.step(acc)
        param_before = emb.get_row(1)
        mom_before = emb.get_momentum(1)
        version_before = emb.router.version_of(1)
        emb.migrate_row(1, 0)
        self.assertEqual(emb.router.shard_of(1), 0)
        self.assertEqual(emb.router.version_of(1), version_before + 1)
        self.assertNotIn(1, emb.shards[1].params)
        self.assertRowAlmostEqual(emb.shards[0].params[1], param_before)
        self.assertRowAlmostEqual(emb.shards[0].momentum[1], mom_before)
        # Fresh gradients on the new route apply normally.
        acc2 = make_accumulator(emb, [([1], [[1.0, 1.0]])])
        self.assertTrue(opt.step(acc2))
        self.assertEqual(opt.step_count, 2)


if __name__ == "__main__":
    unittest.main()
