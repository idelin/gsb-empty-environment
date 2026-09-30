import unittest
from fractions import Fraction

from timecal import RoundingPolicy, round_fraction


class HalfEvenTest(unittest.TestCase):
    def test_third_of_second_in_microseconds(self):
        # 1/3 s is exactly 1000000/3 us = 333333.333... -> nearest integer
        self.assertEqual(round_fraction(Fraction(1_000_000, 3)), 333333)

    def test_ties_go_to_even(self):
        self.assertEqual(round_fraction(Fraction(1, 2)), 0)
        self.assertEqual(round_fraction(Fraction(3, 2)), 2)
        self.assertEqual(round_fraction(Fraction(5, 2)), 2)
        self.assertEqual(round_fraction(Fraction(7, 2)), 4)

    def test_negative_ties_go_to_even(self):
        self.assertEqual(round_fraction(Fraction(-1, 2)), 0)
        self.assertEqual(round_fraction(Fraction(-3, 2)), -2)
        self.assertEqual(round_fraction(Fraction(-5, 2)), -2)

    def test_negative_non_ties(self):
        self.assertEqual(round_fraction(Fraction(-1, 3)), 0)
        self.assertEqual(round_fraction(Fraction(-2, 3)), -1)
        self.assertEqual(round_fraction(Fraction(-7, 4)), -2)


class OtherPoliciesTest(unittest.TestCase):
    def test_floor_ceiling_trunc_on_negatives(self):
        v = Fraction(-7, 2)  # -3.5
        self.assertEqual(round_fraction(v, RoundingPolicy.FLOOR), -4)
        self.assertEqual(round_fraction(v, RoundingPolicy.CEILING), -3)
        self.assertEqual(round_fraction(v, RoundingPolicy.TOWARD_ZERO), -3)
        self.assertEqual(round_fraction(v, RoundingPolicy.HALF_AWAY), -4)

    def test_exact_values_unchanged_by_any_policy(self):
        for policy in RoundingPolicy:
            self.assertEqual(round_fraction(Fraction(-4), policy), -4)
            self.assertEqual(round_fraction(Fraction(7), policy), 7)


if __name__ == "__main__":
    unittest.main()
