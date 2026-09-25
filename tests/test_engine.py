import random
import threading
import unittest

from masked_matmul import (
    BoundsError,
    ComplementMask,
    Engine,
    ExplicitMask,
    RetractionError,
    SpillError,
    TransactionStateError,
    UnknownVersionError,
    dense_reference,
)
from replay_sample import run as replay_run


_ENGINES = []


def track(eng):
    _ENGINES.append(eng)
    return eng


def tearDownModule():
    for eng in _ENGINES:
        eng.spill_store.cleanup()


def make_engine(rows=3, inner=3, cols=3, mask=None, threshold=8):
    return track(Engine(rows, inner, cols, mask or ExplicitMask(rows, cols),
                        row_threshold=threshold))


class CancellationTest(unittest.TestCase):
    def test_two_paths_cancel_then_retract_restores_nonzero(self):
        eng = make_engine(1, 2, 1)
        with eng.begin() as t:
            t.m_insert(0, 0)
            t.a_insert(0, 0, 1)
            t.b_insert(0, 0, 5)
            t.a_insert(0, 1, 1)
            t.b_insert(1, 0, -5)
        self.assertEqual(eng.query_latest(), ())
        self.assertEqual(eng.index.stats()["total_source_entries"], 2)
        self.assertEqual(eng.index.source_entry(0, 0, 0), (5, 1))
        self.assertEqual(eng.index.source_entry(0, 0, 1), (-5, 1))
        with eng.begin() as t:
            t.a_delete(0, 1, 1)
        self.assertEqual(eng.query_latest(), ((0, 0, 5),))
        eng.index.check_against(eng.A, eng.B, eng.mask)

    def test_duplicate_insert_counts_and_partial_retract(self):
        eng = make_engine(1, 1, 1, mask=ComplementMask(1, 1))
        with eng.begin() as t:
            t.a_insert(0, 0, 2)
            t.a_insert(0, 0, 2)
            t.b_insert(0, 0, 3)
        self.assertEqual(eng.query_latest(), ((0, 0, 12),))
        self.assertEqual(eng.index.source_entry(0, 0, 0), (12, 2))
        with eng.begin() as t:
            t.a_delete(0, 0, 2)
        self.assertEqual(eng.query_latest(), ((0, 0, 6),))
        self.assertEqual(eng.index.source_entry(0, 0, 0), (6, 1))

    def test_mismatched_retraction_raises(self):
        eng = make_engine(2, 2, 2, mask=ComplementMask(2, 2))
        with eng.begin() as t:
            t.a_insert(0, 0, 2)
        txn = eng.begin()
        with self.assertRaises(RetractionError):
            txn.a_delete(0, 0, 3)
        txn.abort()
        txn2 = eng.begin()
        with self.assertRaises(RetractionError):
            txn2.a_delete(1, 1, 1)
        txn2.abort()
        self.assertEqual(eng.query_latest(), ())
        self.assertEqual(eng.A.nnz(), 1)


class MaskVisibilityTest(unittest.TestCase):
    def test_mask_delete_only_affects_visibility(self):
        eng = make_engine(2, 2, 2)
        with eng.begin() as t:
            t.m_insert(0, 1)
            t.a_insert(0, 0, 2)
            t.b_insert(0, 1, 4)
        self.assertEqual(eng.query_latest(), ((0, 1, 8),))
        with eng.begin() as t:
            t.m_delete(0, 1)
        self.assertEqual(eng.query_latest(), ())
        self.assertEqual(eng.index.stats()["hidden_source_entries"], 1)
        with eng.begin() as t:
            t.a_insert(0, 0, 1)
        self.assertEqual(eng.query_latest(), ())
        with eng.begin() as t:
            t.m_insert(0, 1)
        self.assertEqual(eng.query_latest(), ((0, 1, 12),))
        eng.index.check_against(eng.A, eng.B, eng.mask)

    def test_mask_delete_with_intervening_updates_and_final_removal(self):
        eng = make_engine(2, 2, 2)
        with eng.begin() as t:
            t.m_insert(1, 1)
            t.a_insert(1, 0, 3)
            t.b_insert(0, 1, 2)
        with eng.begin() as t:
            t.m_delete(1, 1)
        with eng.begin() as t:
            t.b_insert(0, 1, 1)
            t.a_delete(1, 0, 3)
        self.assertEqual(eng.query_latest(), ())
        self.assertEqual(eng.index.stats()["total_source_entries"], 0)
        with eng.begin() as t:
            t.m_insert(1, 1)
        self.assertEqual(eng.query_latest(), ())
        eng.index.check_against(eng.A, eng.B, eng.mask)


class VersionBindingTest(unittest.TestCase):
    def test_query_binds_full_version_vector(self):
        eng = make_engine(1, 1, 1)
        v0 = eng.latest
        with eng.begin() as t:
            t.m_insert(0, 0)
            t.a_insert(0, 0, 1)
        v1 = eng.latest
        self.assertEqual(v1, (1, 0, 1))
        with eng.begin() as t:
            t.b_insert(0, 0, 2)
        v2 = eng.latest
        self.assertEqual(v2, (1, 1, 1))
        self.assertEqual(eng.query(v0), ())
        self.assertEqual(eng.query(v1), ())
        self.assertEqual(eng.query(v2), ((0, 0, 2),))
        with self.assertRaises(UnknownVersionError):
            eng.query((0, 1, 0))
        with self.assertRaises(UnknownVersionError):
            eng.query((9, 9, 9))

    def test_old_snapshot_preserved_under_concurrent_reads(self):
        eng = make_engine(2, 2, 2, mask=ComplementMask(2, 2))
        with eng.begin() as t:
            t.a_insert(0, 0, 1)
            t.b_insert(0, 0, 4)
        v1 = eng.latest
        snap1 = eng.query(v1)
        self.assertEqual(snap1, ((0, 0, 4),))
        errors = []
        stop = threading.Event()

        def reader():
            try:
                while not stop.is_set():
                    if eng.query(v1) != snap1:
                        raise AssertionError("旧快照被污染")
            except Exception as exc:
                errors.append(exc)

        thread = threading.Thread(target=reader)
        thread.start()
        for n in range(30):
            with eng.begin() as t:
                t.a_insert(1, 1, n + 1)
                t.b_insert(1, 1, 2)
        stop.set()
        thread.join()
        self.assertEqual(errors, [])
        self.assertEqual(eng.query(v1), snap1)
        self.assertNotEqual(eng.query_latest(), snap1)


class FailureRecoveryTest(unittest.TestCase):
    def test_abort_rolls_back_and_keeps_published(self):
        eng = make_engine(2, 2, 2)
        with eng.begin() as t:
            t.m_insert(0, 0)
            t.a_insert(0, 0, 2)
            t.b_insert(0, 0, 3)
        v1 = eng.latest
        snap1 = eng.query(v1)
        txn = eng.begin()
        txn.a_insert(1, 1, 5)
        txn.b_insert(1, 1, 5)
        txn.m_insert(1, 1)
        txn.abort()
        self.assertEqual(eng.latest, v1)
        self.assertEqual(eng.query(v1), snap1)
        self.assertEqual(eng.query_latest(), snap1)
        self.assertEqual(eng.A.nnz(), 1)
        self.assertEqual(eng.B.nnz(), 1)
        eng.index.check_against(eng.A, eng.B, eng.mask)

    def test_spill_failure_rolls_back_and_keeps_published(self):
        eng = track(Engine(1, 10, 1, ComplementMask(1, 1), row_threshold=2))
        with eng.begin() as t:
            t.a_insert(0, 0, 1)
            t.b_insert(0, 0, 7)
        v1 = eng.latest
        snap1 = eng.query(v1)
        self.assertEqual(snap1, ((0, 0, 7),))
        eng.spill_store.fail_writes = True
        txn = eng.begin()
        with self.assertRaises(SpillError):
            for k in range(1, 6):
                txn.b_insert(k, 0, 1)
                txn.a_insert(0, k, 1)
        self.assertEqual(txn.state, "aborted")
        eng.spill_store.fail_writes = False
        self.assertEqual(eng.latest, v1)
        self.assertEqual(eng.query(v1), snap1)
        self.assertEqual(eng.query_latest(), snap1)
        self.assertEqual(eng.A.nnz(), 1)
        self.assertEqual(eng.B.nnz(), 1)
        eng.index.check_against(eng.A, eng.B, eng.mask)
        with eng.begin() as t:
            for k in range(1, 6):
                t.b_insert(k, 0, 1)
                t.a_insert(0, k, 1)
        self.assertEqual(eng.query_latest(), ((0, 0, 12),))
        eng.index.check_against(eng.A, eng.B, eng.mask)

    def test_context_manager_aborts_on_exception(self):
        eng = make_engine(1, 1, 1, mask=ComplementMask(1, 1))
        v0 = eng.latest
        with self.assertRaises(RuntimeError):
            with eng.begin() as t:
                t.a_insert(0, 0, 1)
                raise RuntimeError("业务异常")
        self.assertEqual(eng.latest, v0)
        self.assertEqual(eng.A.nnz(), 0)

    def test_single_active_transaction(self):
        eng = make_engine()
        txn = eng.begin()
        with self.assertRaises(TransactionStateError):
            eng.begin()
        txn.abort()
        with self.assertRaises(TransactionStateError):
            txn.commit()


class ComplementMaskTest(unittest.TestCase):
    def test_huge_dims_without_dense_materialization(self):
        n = 10 ** 7
        mask = ComplementMask(n, n)
        eng = track(Engine(n, n, n, mask))
        with eng.begin() as t:
            t.a_insert(5, 7, 2)
            t.b_insert(7, 9, 3)
        self.assertEqual(eng.query_latest(), ((5, 9, 6),))
        with eng.begin() as t:
            t.m_delete(5, 9)
        self.assertEqual(eng.query_latest(), ())
        self.assertEqual(mask.stored_entries(), 1)
        with eng.begin() as t:
            t.m_insert(5, 9)
        self.assertEqual(eng.query_latest(), ((5, 9, 6),))
        self.assertEqual(mask.stored_entries(), 0)
        eng.index.check_against(eng.A, eng.B, eng.mask)

    def test_bounds_checked_against_explicit_dims(self):
        eng = track(Engine(2, 2, 2, ComplementMask(2, 2)))
        txn = eng.begin()
        with self.assertRaises(BoundsError):
            txn.a_insert(2, 0, 1)
        txn.abort()
        txn = eng.begin()
        with self.assertRaises(BoundsError):
            txn.m_delete(0, 5)
        txn.abort()


class HotRowSpillTest(unittest.TestCase):
    def test_hot_row_spills_and_stays_correct(self):
        rows, inner, cols = 2, 60, 40
        mask = ComplementMask(rows, cols)
        eng = track(Engine(rows, inner, cols, mask, row_threshold=16))
        with eng.begin() as t:
            for k in range(inner):
                t.a_insert(0, k, 1)
            for k in range(inner):
                for j in range(cols):
                    if (k * cols + j) % 7 != 0:
                        t.b_insert(k, j, 1)
        stats = eng.index.stats()
        self.assertGreater(stats["spilled_rows"], 0)
        resident_rows = max(1, stats["resident_rows"])
        self.assertLessEqual(
            stats["resident_source_entries"], 16 * resident_rows
        )
        self.assertEqual(
            eng.query_latest(), dense_reference(eng.A, eng.B, mask)
        )
        eng.index.check_against(eng.A, eng.B, mask)
        with eng.begin() as t:
            for k in range(inner):
                t.a_delete(0, k, 1)
            for k in range(inner):
                for j in range(cols):
                    if (k * cols + j) % 7 != 0:
                        t.b_delete(k, j, 1)
        self.assertEqual(eng.query_latest(), ())
        self.assertEqual(eng.index.stats()["total_source_entries"], 0)
        self.assertEqual(eng.spill_store.keys(), [])


class MixedUpdateReferenceTest(unittest.TestCase):
    def test_random_mixed_updates_match_dense_and_leave_no_orphans(self):
        rng = random.Random(20260925)
        rows, inner, cols = 5, 4, 6
        mask = ExplicitMask(rows, cols)
        eng = track(Engine(rows, inner, cols, mask, row_threshold=4))
        live_a, live_b, live_m = {}, {}, {}
        weights = [-3, -2, -1, 1, 2, 3]
        for _step in range(50):
            la = {key: list(ws) for key, ws in live_a.items()}
            lb = {key: list(ws) for key, ws in live_b.items()}
            lm = {key: list(ws) for key, ws in live_m.items()}
            txn = eng.begin()
            for _ in range(rng.randint(1, 6)):
                choices = ["ai", "bi", "mi"]
                if la:
                    choices.append("ad")
                if lb:
                    choices.append("bd")
                if lm:
                    choices.append("md")
                kind = rng.choice(choices)
                if kind == "ai":
                    i, k = rng.randrange(rows), rng.randrange(inner)
                    w = rng.choice(weights)
                    txn.a_insert(i, k, w)
                    la.setdefault((i, k), []).append(w)
                elif kind == "ad":
                    key = rng.choice(sorted(la))
                    w = rng.choice(la[key])
                    txn.a_delete(key[0], key[1], w)
                    la[key].remove(w)
                    if not la[key]:
                        del la[key]
                elif kind == "bi":
                    k, j = rng.randrange(inner), rng.randrange(cols)
                    w = rng.choice(weights)
                    txn.b_insert(k, j, w)
                    lb.setdefault((k, j), []).append(w)
                elif kind == "bd":
                    key = rng.choice(sorted(lb))
                    w = rng.choice(lb[key])
                    txn.b_delete(key[0], key[1], w)
                    lb[key].remove(w)
                    if not lb[key]:
                        del lb[key]
                elif kind == "mi":
                    i, j = rng.randrange(rows), rng.randrange(cols)
                    txn.m_insert(i, j)
                    lm.setdefault((i, j), []).append(1)
                else:
                    key = rng.choice(sorted(lm))
                    txn.m_delete(key[0], key[1])
                    lm[key].pop()
                    if not lm[key]:
                        del lm[key]
            if rng.random() < 0.2:
                txn.abort()
                continue
            txn.commit()
            live_a, live_b, live_m = la, lb, lm
            self.assertEqual(
                eng.query_latest(), dense_reference(eng.A, eng.B, mask),
                f"第 {_step} 步与稠密参考不一致",
            )
            eng.index.check_against(eng.A, eng.B, mask)
        txn = eng.begin()
        for (i, k), ws in live_a.items():
            for w in ws:
                txn.a_delete(i, k, w)
        for (k, j), ws in live_b.items():
            for w in ws:
                txn.b_delete(k, j, w)
        for (i, j), ws in live_m.items():
            for _ in ws:
                txn.m_delete(i, j)
        txn.commit()
        self.assertEqual(eng.query_latest(), ())
        stats = eng.index.stats()
        self.assertEqual(stats["total_source_entries"], 0)
        self.assertEqual(stats["hidden_cells"], 0)
        self.assertEqual(eng.spill_store.keys(), [])
        self.assertEqual(eng.A.nnz(), 0)
        self.assertEqual(eng.B.nnz(), 0)


class ResultOrderingTest(unittest.TestCase):
    def test_rows_deterministically_sorted(self):
        eng = make_engine(4, 4, 4, mask=ComplementMask(4, 4))
        with eng.begin() as t:
            for i in (3, 0, 2, 1):
                t.a_insert(i, i, 1)
                t.b_insert(i, (i * 3) % 4, 1)
                t.b_insert(i, (i + 1) % 4, 2)
        snapshot = eng.query_latest()
        self.assertEqual(list(snapshot), sorted(snapshot))
        rows = [item[0] for item in snapshot]
        self.assertEqual(rows, sorted(rows))


class ReplaySampleTest(unittest.TestCase):
    def test_replay_sample_is_deterministic_and_self_checked(self):
        first = replay_run()
        second = replay_run()
        self.assertEqual(first, second)
        self.assertEqual(first.splitlines()[-1], "OK")
        self.assertIn("对照一致", first)


if __name__ == "__main__":
    unittest.main()
