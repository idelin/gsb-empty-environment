import unittest

from bootci.quantile import quantile, weighted_quantile_type1


class TestQuantileType7(unittest.TestCase):
    def test_known_values(self):
        # type7: h = (n-1)*p 线性插值
        self.assertAlmostEqual(quantile([1, 2, 3, 4], 0.25, "type7"), 1.75)
        self.assertAlmostEqual(quantile([1, 2, 3, 4], 0.5, "type7"), 2.5)
        self.assertAlmostEqual(quantile([1, 2, 3, 4], 0.75, "type7"), 3.25)

    def test_unsorted_input_and_bounds(self):
        self.assertEqual(quantile([4, 1, 3, 2], 0.0, "type7"), 1)
        self.assertEqual(quantile([4, 1, 3, 2], 1.0, "type7"), 4)

    def test_single_element(self):
        self.assertEqual(quantile([42.0], 0.3, "type7"), 42.0)


class TestQuantileType1(unittest.TestCase):
    def test_known_values(self):
        # type1: 经验 CDF 逆，最小 x 使 F(x) >= p
        self.assertEqual(quantile([1, 2, 3, 4], 0.25, "type1"), 1)
        self.assertEqual(quantile([1, 2, 3, 4], 0.5, "type1"), 2)
        self.assertEqual(quantile([1, 2, 3, 4], 0.75, "type1"), 3)
        self.assertEqual(quantile([1, 2, 3, 4], 1.0, "type1"), 4)
        self.assertEqual(quantile([1, 2, 3, 4], 0.0, "type1"), 1)


class TestWeightedType1(unittest.TestCase):
    def test_known_values(self):
        items = [(1.0, 1), (2.0, 1), (3.0, 2)]  # total = 4
        self.assertEqual(weighted_quantile_type1(items, 0.25, 4), 1.0)
        self.assertEqual(weighted_quantile_type1(items, 0.5, 4), 2.0)
        self.assertEqual(weighted_quantile_type1(items, 0.51, 4), 3.0)
        self.assertEqual(weighted_quantile_type1(items, 0.0, 4), 1.0)
        self.assertEqual(weighted_quantile_type1(items, 1.0, 4), 3.0)


class TestQuantileErrors(unittest.TestCase):
    def test_bad_inputs(self):
        with self.assertRaises(ValueError):
            quantile([], 0.5)
        with self.assertRaises(ValueError):
            quantile([1.0], 1.5)
        with self.assertRaises(ValueError):
            quantile([1.0, 2.0], 0.5, "type-unknown")
        with self.assertRaises(ValueError):
            weighted_quantile_type1([(1.0, 1)], 0.5, 0)


if __name__ == "__main__":
    unittest.main()
