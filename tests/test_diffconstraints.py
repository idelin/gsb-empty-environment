import unittest
from fractions import Fraction
from random import Random

from timefit.diffconstraints import DifferenceConstraints


class FeasibilityTests(unittest.TestCase):
    def test_consistent_bounds(self):
        dc = DifferenceConstraints(2)
        dc.add_bound(1, lo=1, hi=5)
        self.assertTrue(dc.feasible())

    def test_negative_cycle_two_variables(self):
        dc = DifferenceConstraints(2)
        dc.add_difference(0, 1, Fraction(1))
        dc.add_difference(1, 0, Fraction(-2))
        self.assertFalse(dc.feasible())

    def test_negative_cycle_chain(self):
        dc = DifferenceConstraints(4)
        dc.add_difference(1, 2, Fraction(1))
        dc.add_difference(2, 3, Fraction(1))
        dc.add_difference(3, 1, Fraction(-3))
        self.assertFalse(dc.feasible())

    def test_tight_chain_feasible(self):
        dc = DifferenceConstraints(4)
        dc.add_difference(1, 2, Fraction(1))
        dc.add_difference(2, 3, Fraction(1))
        dc.add_difference(3, 1, Fraction(-2))
        self.assertTrue(dc.feasible())


class BoundTests(unittest.TestCase):
    def test_tight_bounds(self):
        dc = DifferenceConstraints(3)
        dc.add_bound(1, lo=1, hi=2)
        dc.add_difference(1, 2, Fraction(3))
        dc.add_difference(2, 1, Fraction(-1))
        self.assertEqual(dc.tight_bound(1), (Fraction(1), Fraction(2)))
        self.assertEqual(dc.tight_bound(2), (Fraction(2), Fraction(5)))

    def test_witness_satisfies_all_constraints(self):
        rng = Random(42)
        for _ in range(50):
            n = rng.randint(2, 6)
            dc = DifferenceConstraints(n)
            for _ in range(rng.randint(1, 12)):
                u, v = rng.randrange(n), rng.randrange(n)
                dc.add_difference(u, v, Fraction(rng.randint(-10, 10)))
            if not dc.feasible():
                continue
            w = dc.witness()
            for u, v, c in dc.edges:
                self.assertLessEqual(w[v] - w[u], c)


if __name__ == "__main__":
    unittest.main()
