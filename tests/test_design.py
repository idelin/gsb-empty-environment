import unittest

from binomial_power import exact_power, min_sample_size


class TestMinSampleSize(unittest.TestCase):
    def test_adjacent_point_evidence_brackets_target(self):
        res = min_sample_size(0.5, 0.6, alpha=0.05, target_power=0.8)
        self.assertGreaterEqual(res.power_at_n_min, 0.8)
        self.assertLess(res.power_below, 0.8)
        self.assertEqual(res.n_below, res.n_min - 1)
        self.assertIn(str(res.n_min), res.evidence)

    def test_matches_brute_force_minimum(self):
        res = min_sample_size(0.5, 0.65, alpha=0.05, target_power=0.8)
        for n in range(1, res.n_min):
            self.assertLess(exact_power(n, 0.5, 0.65, 0.05)[2], 0.8, msg=f"n={n}")
        self.assertGreaterEqual(exact_power(res.n_min, 0.5, 0.65, 0.05)[2], 0.8)

    def test_size_constraint_holds(self):
        res = min_sample_size(0.3, 0.5, alpha=0.05, target_power=0.9)
        self.assertLessEqual(res.actual_size, 0.05)

    def test_unreachable_target_raises(self):
        with self.assertRaises(ValueError):
            min_sample_size(0.5, 0.5001, alpha=0.05, target_power=0.99, n_max=200)

    def test_invalid_inputs(self):
        with self.assertRaises(ValueError):
            min_sample_size(0.6, 0.5)  # p1 must exceed p0
        with self.assertRaises(ValueError):
            min_sample_size(0.5, 0.6, target_power=1.5)


if __name__ == "__main__":
    unittest.main()
