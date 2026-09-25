import random
import tempfile
import threading
import unittest
from pathlib import Path

from masked_product import (
    BatchCancelled,
    MaskedABProduct,
    SpillError,
    VersionMismatch,
    VersionVector,
    dense_reference,
)


class MaskedProductTests(unittest.TestCase):
    def make(self, n=3, p=3, m=3, **kwargs):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        return MaskedABProduct(n, p, m, spill_dir=Path(tmp.name) / "spill", **kwargs), tmp

    def dense(self, engine):
        a, b, mask = engine.export_inputs()
        return dense_reference(a, b, mask, engine.n, engine.p, engine.m)

    def assertDense(self, engine):
        expected = self.dense(engine)
        got = [[0] * engine.p for _ in range(engine.n)]
        for i, j, value in engine.query():
            got[i][j] = value
        self.assertEqual(got, expected)
        engine.check_integrity()

    def test_cancel_and_withdraw_restores_cancelled_pair(self):
        engine, _ = self.make(2, 2, 2)
        with engine.batch() as b:
            b.set_a(0, 0, 1).set_a(0, 1, -1)
            b.set_b(0, 0, 2).set_b(1, 0, 2)
            b.add_mask(0, 0).add_mask(1, 0)
        self.assertEqual(engine.query(), [(0, 0, 0)])
        self.assertEqual(engine.contribution_sources(0, 0), 2)
        with engine.batch() as b:
            b.delete_a(0, 1)
        self.assertEqual(engine.query(), [(0, 0, 2)])
        self.assertEqual(engine.contribution_sources(0, 0), 1)

    def test_mask_delete_is_only_visibility_and_rejoin_restores(self):
        engine, _ = self.make(1, 2, 2)
        with engine.batch() as b:
            b.set_a(0, 0, 3).set_b(0, 0, 2)
            b.add_mask(0, 0)
        with engine.batch() as b:
            b.remove_mask(0, 0)
        self.assertEqual(engine.query(), [])
        with engine.batch() as b:
            b.add_mask(0, 0)
        self.assertEqual(engine.query(), [(0, 0, 6)])

    def test_batch_is_atomic_on_user_exception_and_cancel(self):
        engine, _ = self.make(1, 1, 1)
        with self.assertRaises(RuntimeError):
            with engine.batch() as b:
                b.set_a(0, 0, 5).set_b(0, 0, 6).add_mask(0, 0)
                raise RuntimeError("stop")
        self.assertEqual(engine.versions, VersionVector(1, 1, 1))
        self.assertEqual(engine.query(), [])
        with self.assertRaises(BatchCancelled):
            with engine.batch() as b:
                b.set_a(0, 0, 9)
                b.cancel()
        self.assertEqual(engine.query(), [])

    def test_old_snapshot_remains_after_new_commit_sorted(self):
        engine, _ = self.make(2, 2, 2)
        with engine.batch() as b:
            b.set_a(1, 0, 2).set_a(0, 0, 4)
            b.set_b(0, 1, 3).set_b(0, 0, 1)
            b.add_mask(0, 1).add_mask(0, 0)
        old = engine.snapshot()
        with engine.batch() as b:
            b.set_a(0, 0, 5)
        self.assertEqual(old.rows(), [(0, 0, 4), (0, 1, 12), (1, 0, 2), (1, 1, 6)])
        self.assertEqual(engine.query(), [(0, 0, 5), (0, 1, 15), (1, 0, 2), (1, 1, 6)])
        self.assertEqual(engine.query(old.version), old.rows())
        with self.assertRaises(VersionMismatch):
            engine.query((1, 1, 2))
        old.close()

    def test_spill_failure_leaves_published_result(self):
        engine, tmp = self.make(2, 3, 2, row_threshold=2, memory_contributions=10)
        with engine.batch() as b:
            b.set_a(0, 0, 1).set_b(0, 0, 1).add_mask(0, 0)
        original = engine.hot.stage_memory_hot

        def fail(*args, **kwargs):
            if not hasattr(fail, "failed"):
                fail.failed = True
                raise OSError("disk full")
            return original(*args, **kwargs)

        engine.hot.stage_memory_hot = fail
        with self.assertRaises(SpillError):
            with engine.batch() as b:
                b.set_a(0, 0, 2).set_a(1, 0, 2).set_a(1, 1, 2)
                b.set_b(0, 1, 3).set_b(0, 2, 3).set_b(1, 0, 3)
                b.add_mask(0, 1).add_mask(0, 2).add_mask(1, 0)
        self.assertEqual(engine.query(), [(0, 0, 1)])
        engine.hot.stage_memory_hot = original
        with engine.batch() as b:
            b.set_a(0, 0, 2)
        self.assertEqual(engine.query(), [(0, 0, 2)])

    def test_hot_rows_spill_and_remain_bounded(self):
        engine, _ = self.make(2, 8, 3, row_threshold=2, memory_contributions=3)
        with engine.batch() as b:
            for j in range(5):
                b.set_b(0, j, 1).add_mask(0, j)
            b.set_a(0, 0, 1)
        self.assertGreaterEqual(engine.spilled_row_count, 1)
        self.assertLessEqual(engine.memory_contribution_count, 3)
        self.assertEqual(engine.query(), [(0, j, 1) for j in range(5)])
        with engine.batch() as b:
            for j in range(5):
                b.delete_b(0, j)
        self.assertEqual(engine.query(), [])
        self.assertLessEqual(engine.memory_contribution_count, 3)

    def test_random_mixed_transactions_match_dense_and_no_orphans(self):
        random.seed(260925)
        engine, _ = self.make(4, 4, 3, row_threshold=3, memory_contributions=8)
        a, b, mask = {}, {}, set()
        ops = []
        for _ in range(80):
            kind = random.randrange(6)
            i, k, j = random.randrange(4), random.randrange(3), random.randrange(4)
            value = random.choice((-3, -2, -1, 1, 2, 3))
            if kind == 0:
                if value == 0:
                    a.pop((i, k), None)
                else:
                    a[(i, k)] = value
                engine.batch().set_a(i, k, value).commit()
                ops.append(("a", i, k, value))
            elif kind == 1:
                a.pop((i, k), None)
                engine.batch().delete_a(i, k).commit()
                ops.append(("da", i, k))
            elif kind == 2:
                b[(k, j)] = value
                engine.batch().set_b(k, j, value).commit()
                ops.append(("b", k, j, value))
            elif kind == 3:
                b.pop((k, j), None)
                engine.batch().delete_b(k, j).commit()
                ops.append(("db", k, j))
            elif kind == 4:
                mask.add((k, j))
                engine.batch().add_mask(k, j).commit()
                ops.append(("m+", k, j))
            else:
                mask.discard((k, j))
                engine.batch().remove_mask(k, j).commit()
                ops.append(("m-", k, j))
            self.assertDense(engine)
        self.assertLessEqual(engine.memory_contribution_count, 8)
        self.assertEqual(engine.export_inputs(), (a, b, mask))

    def test_concurrent_reader_keeps_pinned_old_result(self):
        engine, _ = self.make(2, 2, 2)
        with engine.batch() as b:
            b.set_a(0, 0, 1).set_b(0, 0, 1).add_mask(0, 0)
        old = engine.snapshot()
        holding = threading.Event()
        release_writer = threading.Event()
        errors = []
        original_finish = engine._finish_commit

        def writer():
            try:
                with engine.batch() as b:
                    b.set_a(0, 0, 7)
            except Exception as exc:
                errors.append(exc)

        def hold_commit(**kwargs):
            holding.set()
            release_writer.wait(1)
            return original_finish(**kwargs)

        engine._finish_commit = hold_commit
        thread = threading.Thread(target=writer)
        thread.start()
        holding.wait(1)
        self.assertEqual(old.rows(), [(0, 0, 1)])
        release_writer.set()
        thread.join(1)
        self.assertFalse(thread.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual(engine.query(), [(0, 0, 7)])
        old.close()

    def test_everything_spilled_threshold_one(self):
        random.seed(11311)
        engine, _ = self.make(3, 3, 2, row_threshold=1, memory_contributions=1)
        a, b, mask = {}, {}, set()
        for t in range(60):
            i, k, j = random.randrange(3), random.randrange(2), random.randrange(3)
            value = random.choice((-2, -1, 1, 2))
            kind = t % 6
            with engine.batch() as batch:
                if kind == 0:
                    a[(i, k)] = value
                    batch.set_a(i, k, value)
                elif kind == 1:
                    a.pop((i, k), None)
                    batch.delete_a(i, k)
                elif kind == 2:
                    b[(k, j)] = value
                    batch.set_b(k, j, value)
                elif kind == 3:
                    b.pop((k, j), None)
                    batch.delete_b(k, j)
                elif kind == 4:
                    mask.add((k, j))
                    batch.add_mask(k, j)
                else:
                    mask.discard((k, j))
                    batch.remove_mask(k, j)
            self.assertDense(engine)
            self.assertLessEqual(engine.memory_contribution_count, 1)

    def test_repeated_updates_do_not_accumulate_orphans(self):
        engine, _ = self.make(1, 2, 2, row_threshold=1, memory_contributions=1)
        with engine.batch() as b:
            b.set_a(0, 0, 1).set_a(0, 1, -1)
            b.set_b(0, 0, 1).set_b(1, 0, 1)
            b.add_mask(0, 0).add_mask(1, 0)
        self.assertEqual(engine.active_contribution_count, 2)
        for value in range(1, 20):
            with engine.batch() as b:
                b.set_a(0, 0, value)
            self.assertEqual(engine.active_contribution_count, 2)
            self.assertEqual(engine.contribution_sources(0, 0), 2)
            self.assertEqual(engine.query(), [(0, 0, value - 1)])

    def test_noop_transaction_reuses_published_version(self):
        engine, _ = self.make(1, 1, 1)
        with engine.batch() as b:
            b.set_a(0, 0, 2).set_b(0, 0, 3).add_mask(0, 0)
        version = engine.current_version
        with engine.batch() as b:
            b.set_a(0, 0, 2).add_mask(0, 0)
        self.assertIs(engine.current_version, version)
        self.assertEqual(engine.query(), [(0, 0, 6)])

    def test_cancelled_batch_leaves_no_private_spill(self):
        engine, tmp = self.make(1, 2, 2)
        with self.assertRaises(BatchCancelled):
            with engine.batch() as b:
                b.set_a(0, 0, 5).set_b(0, 0, 5).add_mask(0, 0)
                b.cancel()
        self.assertEqual(engine.query(), [])
        leftovers = [
            path.name
            for path in Path(tmp.name).rglob("*")
            if path.name.startswith("delta-")
        ]
        self.assertEqual(leftovers, [])


if __name__ == "__main__":
    unittest.main()
