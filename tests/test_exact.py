import unittest
from fractions import Fraction

from bootstrap_ci import (
    EnumerationTooLargeError,
    IIDPlan,
    MovingBlockPlan,
    PairedPlan,
    RandomSource,
    StratifiedPlan,
    UnsupportedMethodError,
    bootstrap,
    bootstrap_exact,
)


def mean(values):
    return sum(values) / len(values)


class TestExactEnumeration(unittest.TestCase):
    def test_hand_computed_distribution_n2(self):
        # Values {0, 1}: resample means are 0 (w 1/4), 1/2 (w 1/2), 1 (w 1/4).
        result = bootstrap_exact(IIDPlan([0.0, 1.0]), mean, alpha=0.5)
        self.assertEqual(result.kind, "exact")
        self.assertEqual(result.support_size, 3)
        self.assertFalse(result.degenerate)
        # inverse-CDF: Q(0.25) = 0, Q(0.75) = 0.5
        self.assertEqual(result.lower, 0.0)
        self.assertEqual(result.upper, 0.5)

    def test_hand_computed_distribution_n3(self):
        # Values {0, 0, 1}: multiset means: 0 (w 8/27), 1/3 (w 12/27),
        # 2/3 (w 6/27), 1 (w 1/27).
        result = bootstrap_exact(IIDPlan([0.0, 0.0, 1.0]), mean, alpha=0.2)
        self.assertEqual(result.support_size, 4)
        # cumulative: 8/27 ~= 0.296 >= 0.1 -> Q(0.1) = 0
        self.assertEqual(result.lower, 0.0)
        # cumulative to 2/3: 26/27 ~= 0.963 >= 0.9 -> Q(0.9) = 2/3
        self.assertAlmostEqual(result.upper, 2.0 / 3.0)

    def test_exact_degenerate_point_interval(self):
        result = bootstrap_exact(IIDPlan([3.0, 3.0, 3.0]), mean)
        self.assertTrue(result.degenerate)
        self.assertEqual(result.support_size, 1)
        self.assertEqual((result.lower, result.upper), (3.0, 3.0))

    def test_exact_paired_plan(self):
        # Constant within-pair difference -> every resample gives 2.0.
        plan = PairedPlan([(0.0, 2.0), (1.0, 3.0), (5.0, 7.0)])
        diff = lambda ps: sum(b - a for a, b in ps) / len(ps)
        result = bootstrap_exact(plan, diff)
        self.assertEqual(result.plan, "paired")
        self.assertTrue(result.degenerate)
        self.assertEqual((result.lower, result.upper), (2.0, 2.0))

    def test_exact_result_records_sample_size(self):
        result = bootstrap_exact(IIDPlan([1.0, 2.0, 3.0, 4.0]), mean)
        self.assertEqual(result.n_units, 4)
        self.assertEqual(result.method, "percentile")

    def test_enumeration_budget_enforced(self):
        plan = IIDPlan([float(i) for i in range(12)])
        with self.assertRaises(EnumerationTooLargeError):
            bootstrap_exact(plan, mean, max_support=10)

    def test_unsupported_plans_fail_explicitly(self):
        with self.assertRaises(UnsupportedMethodError):
            bootstrap_exact(StratifiedPlan({"a": [1.0, 2.0]}), mean)
        with self.assertRaises(UnsupportedMethodError):
            bootstrap_exact(MovingBlockPlan([1.0, 2.0, 3.0], block_length=2),
                            mean)


class TestExactVsMonteCarlo(unittest.TestCase):
    def test_monte_carlo_converges_to_exact(self):
        values = [1.0, 2.0, 3.0, 4.0, 5.0]
        exact = bootstrap_exact(IIDPlan(values), mean, alpha=0.05)
        mc = bootstrap(IIDPlan(values), mean, n_resamples=50_000,
                       rng=RandomSource(2024), alpha=0.05)
        self.assertEqual(exact.kind, "exact")
        self.assertEqual(mc.kind, "monte_carlo")
        # Support spacing of the mean is 1/5; allow one spacing of slack.
        self.assertAlmostEqual(exact.lower, mc.lower, delta=0.21)
        self.assertAlmostEqual(exact.upper, mc.upper, delta=0.21)


if __name__ == "__main__":
    unittest.main()
