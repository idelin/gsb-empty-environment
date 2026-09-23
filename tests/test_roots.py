import unittest
from fractions import Fraction

from ratpiece.poly import Poly
from ratpiece.roots import analyze_roots


class TestRoots(unittest.TestCase):
    def test_rational_roots_with_multiplicity(self):
        p = Poly((2, -5, 4, -1))            # (x-1)^2 (x-2) up to sign
        result = analyze_roots(p)
        self.assertEqual([(r.value, r.multiplicity) for r in result.rational],
                         [(Fraction(1), 2), (Fraction(2), 1)])
        self.assertFalse(result.exhausted)

    def test_irrational_roots_isolated(self):
        p = Poly((-2, 0, 1))                # x^2 - 2
        result = analyze_roots(p)
        self.assertEqual(len(result.isolated), 2)
        neg, pos = result.isolated
        self.assertLess(neg.lo, -1)
        self.assertGreater(neg.hi, -2)
        self.assertLess(pos.lo, 2)
        self.assertGreater(pos.hi, 1)
        # 每个隔离区间确实各含一个根：端点符号相反
        self.assertEqual(p.sign_at(neg.lo) * p.sign_at(neg.hi), -1)
        self.assertEqual(p.sign_at(pos.lo) * p.sign_at(pos.hi), -1)

    def test_mixed_roots(self):
        # (x - 1)(x^2 - 2) = x^3 - x^2 - 2x + 2
        p = Poly((2, -2, -1, 1))
        result = analyze_roots(p)
        self.assertEqual([r.value for r in result.rational], [Fraction(1)])
        self.assertEqual(len(result.isolated), 2)

    def test_budget_exhaustion_marks_unknown(self):
        p = Poly((-2, 0, 1))
        result = analyze_roots(p, budget=0)
        self.assertTrue(result.exhausted)
        self.assertEqual(len(result.unknown), 1)
        self.assertEqual(result.unknown[0].count, 2)
        self.assertEqual(result.isolated, [])

    def test_no_real_roots(self):
        p = Poly((1, 0, 1))                 # x^2 + 1
        result = analyze_roots(p)
        self.assertEqual(result.rational, [])
        self.assertEqual(result.isolated, [])
        self.assertFalse(result.exhausted)


if __name__ == "__main__":
    unittest.main()

