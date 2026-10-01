import math
import random
import unittest

from randomized_response.channels import (
    RandomizedResponseChannel,
    validate_distribution,
    validate_epsilon,
    validate_k,
)


class TestParameterValidation(unittest.TestCase):
    def test_epsilon_must_be_nonnegative_finite_real(self):
        for bad in (-1.0, -0.5):
            with self.assertRaises(ValueError):
                validate_epsilon(bad)
        for bad in (math.inf, math.nan):
            with self.assertRaises(ValueError):
                validate_epsilon(bad)
        for bad in ("1.0", None, True):
            with self.assertRaises(TypeError):
                validate_epsilon(bad)
        self.assertEqual(validate_epsilon(0), 0.0)
        self.assertEqual(validate_epsilon(2), 2.0)

    def test_k_must_be_positive_integer(self):
        for bad in (0, -3):
            with self.assertRaises(ValueError):
                validate_k(bad)
        for bad in (2.5, "4", True):
            with self.assertRaises(TypeError):
                validate_k(bad)
        self.assertEqual(validate_k(1), 1)
        self.assertEqual(validate_k(8), 8)

    def test_distribution_validation(self):
        with self.assertRaises(ValueError):
            validate_distribution([0.5, 0.6], 2)  # sums to 1.1
        with self.assertRaises(ValueError):
            validate_distribution([1.2, -0.2], 2)  # negative entry
        with self.assertRaises(ValueError):
            validate_distribution([1.0], 3)  # wrong length
        validate_distribution([0.25, 0.75], 2)


class TestChannelMatrix(unittest.TestCase):
    def test_columns_are_probability_distributions(self):
        for k in (1, 2, 3, 5, 10):
            for eps in (0.0, 0.1, 1.0, 5.0):
                a = RandomizedResponseChannel(k, eps).matrix
                self.assertEqual(len(a), k)
                for x in range(k):
                    col_sum = sum(a[y][x] for y in range(k))
                    self.assertAlmostEqual(col_sum, 1.0, places=12)
                    self.assertTrue(all(a[y][x] >= 0.0 for y in range(k)))

    def test_known_probabilities(self):
        ch = RandomizedResponseChannel(k=4, epsilon=1.0)
        e = math.e
        self.assertAlmostEqual(ch.p_stay, e / (e + 3.0))
        self.assertAlmostEqual(ch.q_off, 1.0 / (e + 3.0))

    def test_binary_is_k2(self):
        ch = RandomizedResponseChannel(k=2, epsilon=1.0)
        p = ch.p_stay
        expected = [[p, 1.0 - p], [1.0 - p, p]]
        for row_e, row_a in zip(expected, ch.matrix):
            for e, a in zip(row_e, row_a):
                self.assertAlmostEqual(e, a, places=15)

    def test_k1_special_case(self):
        ch = RandomizedResponseChannel(k=1, epsilon=3.0)
        self.assertEqual(ch.matrix, [[1.0]])
        self.assertEqual(ch.p_stay, 1.0)
        self.assertEqual(ch.q_off, 0.0)
        self.assertTrue(ch.is_degenerate)
        rng = random.Random(0)
        self.assertEqual(ch.sample(0, rng), 0)
        self.assertEqual(ch.output_distribution([1.0]), [1.0])

    def test_epsilon0_output_independent_of_input(self):
        k = 5
        ch = RandomizedResponseChannel(k=k, epsilon=0.0)
        self.assertTrue(ch.is_degenerate)
        for row in ch.matrix:
            for v in row:
                self.assertAlmostEqual(v, 1.0 / k)
        # Every input distribution maps to the uniform output distribution.
        out = ch.output_distribution([0.7, 0.1, 0.1, 0.05, 0.05])
        for v in out:
            self.assertAlmostEqual(v, 1.0 / k)

    def test_sample_bounds(self):
        rng = random.Random(42)
        ch = RandomizedResponseChannel(k=6, epsilon=0.5)
        for _ in range(1000):
            self.assertIn(ch.sample(3, rng), range(6))
        with self.assertRaises(ValueError):
            ch.sample(6, rng)


if __name__ == "__main__":
    unittest.main()
