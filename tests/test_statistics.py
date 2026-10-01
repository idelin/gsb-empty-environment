import random
import unittest

from permtest import ecdf_points, frequencies, ks_statistic, total_variation


class TestFrequencies(unittest.TestCase):
    def test_multiplicity(self):
        self.assertEqual(frequencies([1, 1, 1, 2]), {1: 3, 2: 1})

    def test_weights(self):
        counts = frequencies([1, 1, 2], weights=[0.5, 1.5, 2.0])
        self.assertEqual(counts[1], 2.0)
        self.assertEqual(counts[2], 2.0)

    def test_empty_rejected(self):
        with self.assertRaises(ValueError):
            frequencies([])

    def test_bad_weights_rejected(self):
        with self.assertRaises(ValueError):
            frequencies([1, 2], weights=[1.0])
        with self.assertRaises(ValueError):
            frequencies([1, 2], weights=[1.0, -1.0])
        with self.assertRaises(ValueError):
            frequencies([1, 2], weights=[0.0, 0.0])


class TestEcdf(unittest.TestCase):
    def test_simultaneous_jump_full_multiplicity(self):
        # All three repeated observations jump together at value 1.
        self.assertEqual(ecdf_points([1, 1, 1, 2]), [(1, 0.75), (2, 1.0)])

    def test_weighted_jump(self):
        points = ecdf_points([1, 2, 2], weights=[1.0, 1.0, 3.0])
        self.assertEqual(points, [(1, 0.2), (2, 1.0)])


class TestTotalVariation(unittest.TestCase):
    def test_known_value(self):
        # p_a = {1: .5, 2: .5}, p_b = {2: .5, 3: .5} -> TV = 0.5
        self.assertAlmostEqual(total_variation([1, 2], [2, 3]), 0.5)

    def test_identical_is_zero(self):
        self.assertAlmostEqual(total_variation([1, 1, 2], [2, 1, 1]), 0.0)

    def test_disjoint_is_one(self):
        self.assertAlmostEqual(total_variation([1, 1], [2, 2]), 1.0)

    def test_multiplicity_matters(self):
        # p_a = {1: 2/3, 2: 1/3}, p_b = {1: 1/3, 2: 2/3} -> TV = 1/3
        self.assertAlmostEqual(total_variation([1, 1, 2], [1, 2, 2]), 1 / 3)

    def test_empty_rejected(self):
        with self.assertRaises(ValueError):
            total_variation([], [1])
        with self.assertRaises(ValueError):
            total_variation([1], [])


class TestKsStatistic(unittest.TestCase):
    def test_separated_samples(self):
        self.assertAlmostEqual(ks_statistic([1, 2, 3], [4, 5, 6]), 1.0)

    def test_known_value_with_ties(self):
        # F_a: 2/3 at 1, 1 at 2; F_b: 1/3 at 1, 1 at 2 -> gap 1/3
        self.assertAlmostEqual(ks_statistic([1, 1, 2], [1, 2, 2]), 1 / 3)

    def test_repeated_values_jump_by_full_multiplicity(self):
        # F_a jumps to 3/4 at 1 while F_b stays 0 -> gap 0.75.
        self.assertAlmostEqual(ks_statistic([1, 1, 1, 2], [2, 2, 2, 2]), 0.75)

    def test_weights(self):
        # Weighted F_a: 0.25 at 1, 1 at 2; F_b: 0.75 at 1, 1 at 2 -> gap 0.5
        self.assertAlmostEqual(
            ks_statistic([1, 2], [1, 2], weights_a=[1.0, 3.0], weights_b=[3.0, 1.0]),
            0.5,
        )

    def test_empty_rejected(self):
        with self.assertRaises(ValueError):
            ks_statistic([], [1])


class TestLabelReorderInvariance(unittest.TestCase):
    def test_statistics_invariant_to_input_order(self):
        a = [3, 1, 4, 1, 5, 9, 2, 6]
        b = [2, 7, 1, 8, 2, 8]
        rng = random.Random(99)
        for _ in range(10):
            a2, b2 = a[:], b[:]
            rng.shuffle(a2)
            rng.shuffle(b2)
            self.assertEqual(ks_statistic(a, b), ks_statistic(a2, b2))
            self.assertEqual(total_variation(a, b), total_variation(a2, b2))


if __name__ == "__main__":
    unittest.main()
