import unittest

from binpower.design import min_sample_size
from binpower.exact import exact_power


class TestMinSampleSize(unittest.TestCase):
    def test_adjacent_point_evidence(self):
        res = min_sample_size(0.5, 0.65, 0.05, 0.8)
        # Two adjacent EXACT points bracket the target.
        self.assertLess(res.power_below, res.target_power)
        self.assertGreaterEqual(res.power, res.target_power)
        self.assertEqual(res.n_below, res.n - 1)
        self.assertEqual(res.kind, "exact")

    def test_minimality_against_brute_force(self):
        res = min_sample_size(0.5, 0.65, 0.05, 0.8)
        for n in range(1, res.n):
            self.assertLess(exact_power(n, 0.5, 0.65, 0.05).power, 0.8, msg=f"n={n}")

    def test_size_respects_alpha(self):
        res = min_sample_size(0.3, 0.5, 0.05, 0.9)
        self.assertLessEqual(res.size, 0.05)

    def test_known_value(self):
        # Deterministic exact reference: n=158, c=90 for these parameters.
        res = min_sample_size(0.5, 0.6, 0.05, 0.8)
        self.assertEqual(res.n, 158)
        self.assertEqual(res.critical, 90)
        self.assertAlmostEqual(res.power, 0.8056549358367088, places=12)
        self.assertAlmostEqual(res.power_below, 0.7786885461538902, places=12)

    def test_invalid_inputs(self):
        with self.assertRaises(ValueError):
            min_sample_size(0.5, 0.4, 0.05, 0.8)   # p1 <= p0
        with self.assertRaises(ValueError):
            min_sample_size(0.5, 0.6, 0.05, 1.5)   # bad target
        with self.assertRaises(ValueError):
            min_sample_size(0.5, 0.6, 0.0, 0.8)    # bad alpha

    def test_unreachable_target_raises(self):
        with self.assertRaises(RuntimeError):
            min_sample_size(0.5, 0.5001, 0.05, 0.9, n_max=50)


if __name__ == "__main__":
    unittest.main()
