"""In-process engine semantics tests (oracle-checked)."""

import random
import threading
import unittest

from common import apply_ops_dense, dense_mul, dense_witnesses, oracle_versions, random_script

from sr_engine import (
    BooleanSemiring,
    DimensionError,
    DuplicateTxnError,
    Engine,
    IntSemiring,
    MinPlusSemiring,
    VersionNotRetainedError,
)

SEMIRINGS = [BooleanSemiring, IntSemiring, MinPlusSemiring]


class TestSemantics(unittest.TestCase):
    def test_cross_terms_within_one_transaction(self):
        # A and B entries committed together must meet in C; adding the
        # two deltas separately would miss this product.
        eng = Engine(2, 2, 2, IntSemiring())
        eng.commit("t1", [["set", "A", 0, 0, 2], ["set", "B", 0, 0, 3]])
        self.assertEqual(eng.C, {(0, 0): 6})

    def test_minplus_next_best_after_delete(self):
        eng = Engine(1, 3, 1, MinPlusSemiring())
        eng.commit("t1", [
            ["set", "A", 0, 0, 1], ["set", "A", 0, 1, 1], ["set", "A", 0, 2, 5],
            ["set", "B", 0, 0, 1], ["set", "B", 1, 0, 1], ["set", "B", 2, 0, 1],
        ])
        self.assertEqual(eng.value(0, 0), 2)
        self.assertEqual(eng.witnesses(0, 0), [0, 1])  # ties in ascending k
        eng.commit("t2", [["del", "A", 0, 0]])
        self.assertEqual(eng.value(0, 0), 2)  # second-best survives
        self.assertEqual(eng.witnesses(0, 0), [1])
        eng.commit("t3", [["del", "B", 1, 0]])
        self.assertEqual(eng.value(0, 0), 6)  # falls back to next candidate
        self.assertEqual(eng.witnesses(0, 0), [2])
        eng.commit("t4", [["del", "A", 0, 2]])
        self.assertEqual(eng.C, {})
        self.assertEqual(eng.witnesses(0, 0), [])

    def test_boolean_witness_path_survives(self):
        eng = Engine(1, 2, 1, BooleanSemiring())
        eng.commit("t1", [
            ["set", "A", 0, 0, True], ["set", "A", 0, 1, True],
            ["set", "B", 0, 0, True], ["set", "B", 1, 0, True],
        ])
        self.assertEqual(eng.witnesses(0, 0), [0, 1])
        eng.commit("t2", [["del", "A", 0, 0]])
        self.assertEqual(eng.value(0, 0), True)  # other path still proves it
        self.assertEqual(eng.witnesses(0, 0), [1])
        eng.commit("t3", [["del", "B", 1, 0]])
        self.assertEqual(eng.C, {})

    def test_int_cancellation_keeps_canonical_sparsity(self):
        eng = Engine(1, 2, 1, IntSemiring())
        eng.commit("t1", [
            ["set", "A", 0, 0, 1], ["set", "A", 0, 1, -1],
            ["set", "B", 0, 0, 1], ["set", "B", 1, 0, 1],
        ])
        self.assertEqual(eng.C, {})  # 1 + (-1) cancels: no explicit zero
        eng.commit("t2", [["set", "B", 1, 0, 2]])
        self.assertEqual(eng.C, {(0, 0): -1})  # stale contributions not lost
        eng.commit("t3", [["set", "A", 0, 0, 0]])  # explicit zero == delete
        self.assertEqual(eng.C, {(0, 0): -2})
        self.assertNotIn((0, 0), eng.A)

    def test_dimension_and_type_validation(self):
        eng = Engine(2, 3, 2, IntSemiring())
        with self.assertRaises(DimensionError):
            eng.commit("bad1", [["set", "A", 2, 0, 1]])  # row overflow
        with self.assertRaises(DimensionError):
            eng.commit("bad2", [["set", "A", 0, 3, 1]])  # k overflow on A
        with self.assertRaises(DimensionError):
            eng.commit("bad3", [["set", "B", 3, 0, 1]])  # k overflow on B
        with self.assertRaises(DimensionError):
            eng.commit("bad4", [["set", "B", 0, -1, 1]])
        with self.assertRaises(TypeError):
            eng.commit("bad5", [["set", "A", 0, 0, 1.5]])
        with self.assertRaises(TypeError):
            Engine(1, 1, 1, BooleanSemiring()).commit(
                "bad6", [["set", "A", 0, 0, 1]])

    def test_rollback_on_invalid_transaction(self):
        eng = Engine(2, 2, 2, IntSemiring())
        eng.commit("ok", [["set", "A", 0, 0, 7]])
        before = (dict(eng.A), dict(eng.B), dict(eng.C), eng.version)
        with self.assertRaises(DimensionError):
            eng.commit("bad", [["set", "B", 0, 0, 1], ["set", "A", 9, 9, 1]])
        after = (dict(eng.A), dict(eng.B), dict(eng.C), eng.version)
        self.assertEqual(before, after)  # nothing applied, version untouched

    def test_duplicate_coordinates_merge_last_wins(self):
        eng = Engine(1, 1, 1, IntSemiring())
        eng.commit("t", [["set", "A", 0, 0, 1], ["set", "A", 0, 0, 2],
                         ["set", "B", 0, 0, 5]])
        self.assertEqual(eng.A, {(0, 0): 2})
        self.assertEqual(eng.C, {(0, 0): 10})

    def test_txn_id_idempotency(self):
        eng = Engine(1, 1, 1, IntSemiring())
        ops = [["set", "A", 0, 0, 4]]
        v1 = eng.commit("x", ops)
        self.assertEqual(eng.commit("x", ops), v1)  # same content: replay ok
        self.assertEqual(eng.version, v1)
        with self.assertRaises(DuplicateTxnError):
            eng.commit("x", [["set", "A", 0, 0, 5]])


class TestOracle(unittest.TestCase):
    def test_random_batches_match_dense_oracle(self):
        rng = random.Random(20261001)
        for sr_cls in SEMIRINGS:
            sr = sr_cls()
            for _ in range(12):
                m, k, n = (rng.randint(1, 5) for _ in range(3))
                txns = random_script(rng, sr, m, k, n, steps=25)
                eng = Engine(m, k, n, sr)
                for txn in txns:
                    eng.commit(txn["txn_id"], txn["ops"])
                expected = oracle_versions(sr, m, k, n, txns)
                for version, (A, B, C) in enumerate(expected):
                    with eng.snapshot(version) as snap:
                        self.assertEqual(dict(snap.A), A, (sr.name, version))
                        self.assertEqual(dict(snap.B), B, (sr.name, version))
                        self.assertEqual(dict(snap.C), C, (sr.name, version))

    def test_random_witnesses_match_oracle(self):
        rng = random.Random(7)
        for sr_cls in SEMIRINGS:
            sr = sr_cls()
            m, k, n = 3, 4, 3
            txns = random_script(rng, sr, m, k, n, steps=30)
            eng = Engine(m, k, n, sr)
            A, B = {}, {}
            for txn in txns:
                eng.commit(txn["txn_id"], txn["ops"])
                apply_ops_dense(A, B, txn["ops"], sr)
                for _ in range(3):
                    i, j = rng.randrange(m), rng.randrange(n)
                    self.assertEqual(
                        eng.witnesses(i, j),
                        dense_witnesses(A, B, sr, m, k, n, i, j),
                        (sr.name, i, j),
                    )


class TestContributionCounter(unittest.TestCase):
    def test_single_update_touches_only_inverted_index(self):
        eng = Engine(60, 60, 60, IntSemiring())
        ops = []
        for x in range(1, 51):  # 50 unrelated nonzero products on the diagonal
            ops.append(["set", "A", x, x, 1])
            ops.append(["set", "B", x, x, 1])
        for j in range(10):  # B row 0 has exactly 10 entries
            ops.append(["set", "B", 0, j, 1])
        eng.commit("setup", ops)
        self.assertEqual(len(eng.C), 50)
        eng.stats.reset()
        eng.commit("one", [["set", "A", 0, 0, 3]])
        # Only the 10 outputs reachable through B's row 0 are recomputed,
        # nowhere near the 50+ unrelated nonzeros elsewhere.
        self.assertEqual(eng.stats.contrib_updates, 10)
        self.assertEqual(eng.stats.output_recomputes, 10)
        self.assertEqual(len(eng.C), 60)
        eng.stats.reset()
        eng.commit("two", [["del", "A", 0, 0]])
        self.assertEqual(eng.stats.contrib_updates, 10)
        self.assertEqual(eng.stats.output_recomputes, 10)
        self.assertEqual(len(eng.C), 50)


class TestSnapshots(unittest.TestCase):
    def test_pinned_snapshot_survives_gc(self):
        eng = Engine(1, 1, 1, IntSemiring())
        eng.commit("t1", [["set", "A", 0, 0, 1], ["set", "B", 0, 0, 1]])
        snap = eng.snapshot(1)
        eng.commit("t2", [["set", "A", 0, 0, 5]])
        eng.gc()
        self.assertEqual(snap.value(0, 0), 1)  # pinned version still readable
        with self.assertRaises(VersionNotRetainedError):
            eng.snapshot(0)  # unpinned old version collected
        snap.close()
        eng.gc()
        with self.assertRaises(VersionNotRetainedError):
            eng.snapshot(1)  # released pin may now be collected

    def test_concurrent_readers_during_writes(self):
        eng = Engine(4, 4, 4, IntSemiring())
        errors = []
        stop = threading.Event()

        def reader():
            try:
                while not stop.is_set():
                    with eng.snapshot() as snap:
                        # C must always equal A x B for the pinned version.
                        expect = dense_mul(dict(snap.A), dict(snap.B),
                                           eng.semiring, 4, 4, 4)
                        assert dict(snap.C) == expect
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        threads = [threading.Thread(target=reader) for _ in range(4)]
        for t in threads:
            t.start()
        rng = random.Random(3)
        for txn in random_script(rng, eng.semiring, 4, 4, 4, steps=60):
            eng.commit(txn["txn_id"], txn["ops"])
        stop.set()
        for t in threads:
            t.join()
        self.assertEqual(errors, [])


if __name__ == "__main__":
    unittest.main()
