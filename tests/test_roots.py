import unittest
from fractions import Fraction

from rational_pieces.poly import Poly
from rational_pieces.roots import (
    IsolationBudget, count_real_roots, isolate_roots, rational_roots, sign_on_interval,
)


class TestRoots(unittest.TestCase):
    def test_sturm_count(self):
        p = Poly([-2, 0, 1])  # x^2 - 2
        self.assertEqual(count_real_roots(p), 2)
        self.assertEqual(count_real_roots(p, 0, 2), 1)
        self.assertEqual(count_real_roots(p, None, 0), 1)

    def test_isolation_unique_root_verified(self):
        p = Poly([-2, 0, 1])
        iso = isolate_roots(p, budget=IsolationBudget())
        self.assertTrue(iso.complete)
        self.assertEqual(iso.root_count, 2)
        for (a, b) in iso.intervals:
            # 独立核验：Sturm 计数恰为 1，且端点符号相反（单根）
            self.assertEqual(count_real_roots(p, a, b), 1)
            self.assertLessEqual(p.sign_at(a) * p.sign_at(b), 0)
        # sqrt(2) 的隔离区间应夹住 1.4142...
        pos = [iv for iv in iso.intervals if iv[0] >= 0][0]
        self.assertLess(pos[0], Fraction(3, 2))
        self.assertGreater(pos[1], Fraction(7, 5))

    def test_budget_exhaustion_keeps_undecided(self):
        p = Poly([-2, 0, 1]) * Poly([-8, 0, 1])  # 根 ±√2, ±2√2
        iso = isolate_roots(p, 0, 3, IsolationBudget(max_steps=0))
        self.assertFalse(iso.complete)
        self.assertEqual(iso.intervals, [])
        self.assertEqual(len(iso.undecided), 1)
        self.assertEqual(iso.undecided[0][2], 2)  # 两个根未分离

    def test_rational_roots_exact(self):
        self.assertEqual(rational_roots(Poly([-1, 0, 1])), [Fraction(-1), Fraction(1)])
        self.assertEqual(rational_roots(Poly([1, -3, 2])),  # 2x^2-3x+1
                         [Fraction(1, 2), Fraction(1)])
        self.assertEqual(rational_roots(Poly([-1, -1, 2])),  # 2x^2-x-1
                         [Fraction(-1, 2), Fraction(1)])
        self.assertEqual(rational_roots(Poly([1, -3, 0, 2])),  # 2x^3-3x+1
                         [Fraction(1)])
        self.assertEqual(rational_roots(Poly([-2, 0, 1])), [])  # x^2-2 无有理根

    def test_sign_on_interval(self):
        p = Poly([-2, 0, 1])
        self.assertEqual(sign_on_interval(p, 0, 1), -1)
        self.assertIsNone(sign_on_interval(p, 0, 2))  # 区间内有根，符号不恒定


if __name__ == '__main__':
    unittest.main()
