import unittest

from permtest import paired_test, stratified_test, two_sample_test
from permtest.exact import ExactResult
from permtest.montecarlo import MonteCarloResult


class TestTwoSampleAPI(unittest.TestCase):
    def test_auto_exact_for_small_sample(self):
        result = two_sample_test([3, 4], [1, 2])
        self.assertIsInstance(result, ExactResult)
        self.assertAlmostEqual(result.p_value, 1 / 3)

    def test_auto_monte_carlo_for_large_sample(self):
        group1 = list(range(15, 30))
        group2 = list(range(15))
        result = two_sample_test(group1, group2, draws=1000, seed=11,
                                 exact_limit=100)
        self.assertIsInstance(result, MonteCarloResult)
        self.assertEqual(result.seed, 11)

    def test_forced_exact(self):
        result = two_sample_test([3, 4], [1, 2], method="exact")
        self.assertIsInstance(result, ExactResult)

    def test_unequal_groups(self):
        result = two_sample_test([10, 11, 12], [1, 2], alternative="greater")
        self.assertAlmostEqual(result.p_value, 0.1)


class TestPairedAPI(unittest.TestCase):
    def test_exact(self):
        result = paired_test(before=[1, 1, 1], after=[2, 3, 5])
        self.assertIsInstance(result, ExactResult)
        self.assertAlmostEqual(result.p_value, 0.25)

    def test_length_mismatch(self):
        with self.assertRaises(ValueError):
            paired_test(before=[1, 2], after=[1])


class TestStratifiedAPI(unittest.TestCase):
    def test_exact(self):
        result = stratified_test([([3], [1, 2]), ([5, 4], [1, 2])])
        self.assertIsInstance(result, ExactResult)
        self.assertEqual(result.n_total, 3 * 6)

    def test_monte_carlo(self):
        strata = [(list(range(8, 14)), list(range(6))),
                  (list(range(9, 15)), list(range(7)))]
        result = stratified_test(strata, method="monte-carlo", draws=1000,
                                 seed=3)
        self.assertIsInstance(result, MonteCarloResult)


if __name__ == "__main__":
    unittest.main()
