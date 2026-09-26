import statistics
import threading
import unittest

from dpquery import (
    BudgetExhaustedError,
    Predicate,
    PrivateQueryEngine,
    Query,
    SyntheticDataset,
    make_synthetic_dataset,
    make_version_variant,
)
from dpquery.mechanisms import LaplaceMechanism


def count_q(preds) -> Query:
    return Query(kind="count", predicates=tuple(preds))


class CanonicalSpecTests(unittest.TestCase):
    def test_predicate_field_order_is_irrelevant(self):
        p1 = [Predicate("age", ">=", 18), Predicate("city", "==", "Beijing")]
        p2 = [Predicate("city", "==", "Beijing"), Predicate("age", ">=", 18)]
        self.assertEqual(count_q(p1).canonical_spec(), count_q(p2).canonical_spec())

    def test_threshold_change_changes_spec(self):
        a = count_q([Predicate("age", ">=", 18)])
        b = count_q([Predicate("age", ">=", 30)])
        c = count_q([Predicate("age", ">", 18)])
        self.assertNotEqual(a.canonical_spec(), b.canonical_spec())
        self.assertNotEqual(a.canonical_spec(), c.canonical_spec())

    def test_sum_bounds_are_part_of_spec(self):
        q1 = Query(kind="sum", column="income", bounds=(0.0, 50_000.0))
        q2 = Query(kind="sum", column="income", bounds=(0.0, 80_000.0))
        self.assertNotEqual(q1.canonical_spec(), q2.canonical_spec())

    def test_query_validation(self):
        with self.assertRaises(ValueError):
            Query(kind="sum", column="income")  # missing bounds
        with self.assertRaises(ValueError):
            Query(kind="count", column="age")
        with self.assertRaises(ValueError):
            Predicate("age", "~", 1)  # invalid op


class BudgetAndReuseTests(unittest.TestCase):
    def setUp(self):
        self.ds = make_synthetic_dataset(n=200, seed=7)
        self.engine = PrivateQueryEngine(self.ds, total_epsilon=1.0, seed=42)

    def test_identical_query_reuses_no_extra_charge(self):
        q = count_q([Predicate("age", ">=", 18)])
        r1 = self.engine.release(q, epsilon=0.5)
        r2 = self.engine.release(q, epsilon=0.5)
        self.assertFalse(r1.reused)
        self.assertTrue(r2.reused)
        self.assertEqual(r1.value, r2.value)
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.5)

    def test_threshold_change_is_charged_fresh(self):
        q1 = count_q([Predicate("age", ">=", 18)])
        q2 = count_q([Predicate("age", ">=", 30)])
        self.engine.release(q1, epsilon=0.4)
        self.engine.release(q2, epsilon=0.4)
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.8)

    def test_epsilon_change_is_charged_fresh(self):
        q = count_q([Predicate("age", ">=", 18)])
        self.engine.release(q, epsilon=0.3)
        self.engine.release(q, epsilon=0.5)
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.8)

    def test_data_version_change_is_charged_fresh(self):
        q = count_q([Predicate("city", "==", "Beijing")])
        ds2 = make_version_variant()
        e2 = PrivateQueryEngine(ds2, total_epsilon=1.0, seed=42)
        self.engine.release(q, epsilon=0.5)
        e2.release(q, epsilon=0.5)
        self.assertNotEqual(
            self.engine.cache_key(q, 0.5), e2.cache_key(q, 0.5)
        )
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.5)
        self.assertAlmostEqual(e2.accountant.spent_epsilon, 0.5)

    def test_sum_bounds_change_is_charged_fresh(self):
        q1 = Query(kind="sum", column="income", bounds=(0.0, 50_000.0))
        q2 = Query(kind="sum", column="income", bounds=(0.0, 80_000.0))
        self.engine.release(q1, epsilon=0.3)
        self.engine.release(q2, epsilon=0.3)
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.6)

    def test_budget_exhausted_blocks_new_query_but_cache_still_served(self):
        q1 = count_q([Predicate("age", ">=", 18)])
        q2 = count_q([Predicate("age", ">=", 30)])
        self.engine.release(q1, epsilon=0.9)
        with self.assertRaises(BudgetExhaustedError):
            self.engine.release(q2, epsilon=0.5)
        # cached result remains free
        cached = self.engine.release(q1, epsilon=0.9)
        self.assertTrue(cached.reused)
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.9)

    def test_failed_leader_refunds_and_followers_raise(self):
        q = count_q([Predicate("nonexistent_column", "==", 1)])
        barrier = threading.Barrier(4)
        results: list[object] = []

        def worker():
            barrier.wait()
            try:
                results.append(self.engine.release(q, epsilon=0.6))
            except Exception as exc:  # noqa: BLE001
                results.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(len(results), 4)
        self.assertTrue(all(isinstance(r, KeyError) for r in results))
        self.assertEqual(self.engine.cache_size(), 0)
        self.assertAlmostEqual(self.engine.accountant.spent_epsilon, 0.0)


class ConcurrencyTests(unittest.TestCase):
    def test_concurrent_identical_query_publishes_once(self):
        ds = make_synthetic_dataset(n=500, seed=11)
        engine = PrivateQueryEngine(ds, total_epsilon=2.0, seed=123)
        q = count_q([
            Predicate("city", "==", "Shanghai"),
            Predicate("active", "==", True),
        ])
        n = 12
        ready = threading.Barrier(n)
        values: list[float] = []
        lock = threading.Lock()

        def worker():
            ready.wait()
            r = engine.release(q, epsilon=0.7)
            with lock:
                values.append(r.value)

        threads = [threading.Thread(target=worker) for _ in range(n)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(len(set(values)), 1)
        self.assertEqual(engine.cache_size(), 1)
        self.assertAlmostEqual(engine.accountant.spent_epsilon, 0.7)


class MechanismTests(unittest.TestCase):
    def test_seeded_mechanism_is_reproducible(self):
        m1 = LaplaceMechanism(seed=5)
        m2 = LaplaceMechanism(seed=5)
        a = [m1.release(100.0, 1.0, 0.5) for _ in range(5)]
        b = [m2.release(100.0, 1.0, 0.5) for _ in range(5)]
        self.assertEqual(a, b)

    def test_laplace_distribution_is_unbiased_and_scales_with_sensitivity(self):
        m = LaplaceMechanism(seed=99)
        samples = [m.release(0.0, 1.0, 1.0) - 0.0 for _ in range(40_000)]
        self.assertLess(abs(statistics.fmean(samples)), 0.05)
        wide = [LaplaceMechanism(seed=s).release(0.0, 4.0, 1.0) for s in range(40_000)]
        self.assertGreater(statistics.pstdev(wide), statistics.pstdev(samples) * 2)

    def test_zero_sensitivity_adds_no_noise(self):
        m = LaplaceMechanism(seed=1)
        self.assertEqual(m.release(3.5, 0.0, 1.0), 3.5)


class SyntheticOnlyTests(unittest.TestCase):
    def test_real_dataset_rejected(self):
        real = object.__new__(SyntheticDataset)
        object.__setattr__(real, "__dict__", {})
        # construct a lookalike tagged as non-synthetic
        from dataclasses import fields

        kwargs = {f.name: None for f in fields(SyntheticDataset)}
        kwargs.update(
            name="real", version="x", seed=0, records=(), schema={}, kind="real"
        )
        with self.assertRaises(ValueError):
            SyntheticDataset(**kwargs)

    def test_generator_is_deterministic_and_versioned(self):
        a = make_synthetic_dataset(n=100, seed=3)
        b = make_synthetic_dataset(n=100, seed=3)
        c = make_synthetic_dataset(n=101, seed=3)
        self.assertEqual(a.records, b.records)
        self.assertEqual(a.version, b.version)
        self.assertNotEqual(a.version, c.version)


if __name__ == "__main__":
    unittest.main(verbosity=2)
