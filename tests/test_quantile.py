import unittest
from fractions import Fraction

from bootstrap_ci import quantile_type7, weighted_quantile_inverse_cdf


class TestQuantileType7(unittest.TestCase):
    def test_known_values(self):
        data = [1.0, 2.0, 3.0, 4.0]
        self.assertEqual(quantile_type7(data, 0.0), 1.0)
        self.assertEqual(quantile_type7(data, 1.0), 4.0)
        self.assertEqual(quantile_type7(data, 0.5), 2.5)
        # h = 3 * 0.25 = 0.75 -> 1 + 0.75 * (2 - 1)
        self.assertEqual(quantile_type7(data, 0.25), 1.75)

    def test_single_element(self):
        self.assertEqual(quantile_type7([7.0], 0.3), 7.0)

    def test_interpolation_between_points(self):
        data = [0.0, 10.0]
        self.assertAlmostEqual(quantile_type7(data, 0.3), 3.0)

    def test_rejects_bad_p_and_empty(self):
        with self.assertRaises(ValueError):
            quantile_type7([1.0], -0.1)
        with self.assertRaises(ValueError):
            quantile_type7([1.0], 1.1)
        with self.assertRaises(ValueError):
            quantile_type7([], 0.5)


class TestWeightedInverseCDF(unittest.TestCase):
    def setUp(self):
        self.support = [(0.0, Fraction(1, 4)),
                        (1.0, Fraction(1, 2)),
                        (2.0, Fraction(1, 4))]

    def test_inverse_cdf_definition(self):
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 0.0), 0.0)
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 0.25), 0.0)
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 0.26), 1.0)
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 0.75), 1.0)
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 0.76), 2.0)
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 1.0), 2.0)

    def test_exact_rational_boundaries(self):
        # cumulative hits exactly 1/2 at value 1.0 -> Q(1/2) == 1.0
        self.assertEqual(weighted_quantile_inverse_cdf(self.support, 0.5), 1.0)

    def test_rejects_empty(self):
        with self.assertRaises(ValueError):
            weighted_quantile_inverse_cdf([], 0.5)


if __name__ == "__main__":
    unittest.main()
