import unittest

from dp_synth import (
    BudgetExceededError,
    Condition,
    CountQuery,
    DPQueryEngine,
    HistogramQuery,
    Predicate,
    SumQuery,
    make_synthetic_employee_data,
    true_answer,
)


class EngineReuseTests(unittest.TestCase):
    def setUp(self):
        self.data = make_synthetic_employee_data(n=500, seed=11)
        self.engine = DPQueryEngine(total_epsilon_per_version=4.0, rng_seed=3)

    def test_exact_repeat_is_free_and_identical(self):
        q = CountQuery(epsilon=1.0)
        first = self.engine.run(q, self.data)
        self.assertAlmostEqual(self.engine.remaining_budget(self.data.version), 3.0)
        for _ in range(5):
            again = self.engine.run(q, self.data)
            self.assertEqual(again.value, first.value)
        self.assertAlmostEqual(self.engine.remaining_budget(self.data.version), 3.0)
        charges = [e for e in self.engine._ledger(self.data.version).entries if e.kind == "charge"]
        reuses = [e for e in self.engine._ledger(self.data.version).entries if e.kind == "reuse"]
        self.assertEqual(len(charges), 1)
        self.assertEqual(len(reuses), 5)

    def test_threshold_change_consumes_budget_and_redraws(self):
        q0 = CountQuery(epsilon=1.0)
        q1 = CountQuery(epsilon=1.0, threshold=1.0)
        p0 = self.engine.run(q0, self.data)
        p1 = self.engine.run(q1, self.data)
        self.assertNotEqual(p0.value, p1.value)
        self.assertAlmostEqual(self.engine.remaining_budget(self.data.version), 2.0)
        self.assertEqual(len(self.engine.cached_keys(self.data.version)), 2)

    def test_data_version_change_separate_budget_and_cache(self):
        data_b = make_synthetic_employee_data(n=501, seed=11)
        q = CountQuery(epsilon=1.0)
        self.engine.run(q, self.data)
        pb = self.engine.run(q, data_b)
        self.assertAlmostEqual(self.engine.remaining_budget(self.data.version), 3.0)
        self.assertAlmostEqual(self.engine.remaining_budget(data_b.version), 3.0)
        self.assertNotEqual(self.data.version, data_b.version)
        self.assertNotEqual(pb.data_version, self.data.version)

    def test_budget_exhausted_blocks_new_query_but_not_reuse(self):
        q = CountQuery(epsilon=2.0)
        self.engine.run(q, self.data)
        q2 = CountQuery(epsilon=2.0, threshold=1.0)
        self.engine.run(q2, self.data)
        with self.assertRaises(BudgetExceededError):
            self.engine.run(CountQuery(epsilon=1.0, threshold=9.0), self.data)
        reused = self.engine.run(q, self.data)
        self.assertAlmostEqual(self.engine.remaining_budget(self.data.version), 0.0)
        self.assertIsNotNone(reused)

    def test_rejected_run_is_not_cached_and_can_retry(self):
        small = DPQueryEngine(total_epsilon_per_version=0.5, rng_seed=1)
        with self.assertRaises(BudgetExceededError):
            small.run(CountQuery(epsilon=1.0), self.data)
        self.assertEqual(small.cached_keys(self.data.version), [])
        pub = small.run(CountQuery(epsilon=0.5), self.data)
        self.assertEqual(len(small.cached_keys(self.data.version)), 1)
        self.assertAlmostEqual(small.remaining_budget(self.data.version), 0.0)

    def test_sum_sensitivity_and_histogram(self):
        qs = SumQuery("years", 0, 25, Predicate((Condition("active", "==", True),)), epsilon=1.0)
        self.assertEqual(qs.sensitivity, 25.0)
        ps = self.engine.run(qs, self.data)
        self.assertAlmostEqual(ps.value, true_answer(qs, self.data), delta=1000)

        qh = HistogramQuery("segment", epsilon=1.0)
        ph = self.engine.run(qh, self.data)
        again = self.engine.run(qh, self.data)
        self.assertEqual(dict(ph.value), dict(again.value))
        self.assertEqual(set(ph.value), {"free", "basic", "pro", "enterprise"})

    def test_non_synthetic_input_rejected(self):
        with self.assertRaises(TypeError):
            self.engine.run(CountQuery(), [{"x": 1}])

    def test_invalid_epsilon_rejected(self):
        with self.assertRaises(ValueError):
            CountQuery(epsilon=0.0)
        with self.assertRaises(ValueError):
            SumQuery("years", 25, 0, epsilon=1.0)


if __name__ == "__main__":
    unittest.main()
