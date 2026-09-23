import unittest
from fractions import Fraction

from ratpiece.poly import Poly
from ratpiece.rational import RationalFunc


def rf(num, den):
    return RationalFunc(Poly(num), Poly(den))


class TestRationalFunc(unittest.TestCase):
    def test_reduction_and_monic_denominator(self):
        f = rf((2, 2), (2,))                # (2x+2)/2 = x+1
        self.assertEqual(f.num, Poly((1, 1)))
        self.assertEqual(f.den, Poly.const(1))

    def test_domain_hole_and_removable_limit(self):
        f = rf((-1, 0, 1), (-1, 1))         # (x^2-1)/(x-1)
        self.assertEqual(f.rational_exclusions(), [Fraction(1)])
        self.assertFalse(f.is_defined_at(1))
        lim = f.value_or_limit(1)
        self.assertEqual(lim.kind, "value")
        self.assertEqual(lim.value, Fraction(2))
        self.assertTrue(lim.evidence)

    def test_pole_side_signs_odd_order(self):
        f = rf((1,), (-2, 1))               # 1/(x-2)
        left, right = f.side_limits(2)
        self.assertEqual((left.kind, left.sign), ("pole", -1))
        self.assertEqual((right.kind, right.sign), ("pole", 1))

    def test_pole_side_signs_even_order(self):
        f = rf((1,), (4, -4, 1))            # 1/(x-2)^2
        left, right = f.side_limits(2)
        self.assertEqual(left.sign, 1)
        self.assertEqual(right.sign, 1)

    def test_pole_evidence_is_algebraic(self):
        f = rf((1,), (-2, 1))
        left, _ = f.side_limits(2)
        joined = " ".join(left.evidence)
        self.assertIn("重数", joined)

    def test_arithmetic_preserves_holes(self):
        f = rf((-1, 0, 1), (-1, 1))         # hole at 1
        g = rf((1,), (-2, 1))               # pole at 2
        s = f + g
        self.assertEqual(s.rational_exclusions(), [Fraction(1), Fraction(2)])
        m = f * g
        self.assertEqual(m.rational_exclusions(), [Fraction(1), Fraction(2)])
        self.assertFalse(m.is_defined_at(1))

    def test_sum_value_exact(self):
        f = rf((1,), (-2, 1))               # 1/(x-2)
        g = rf((1,), (-3, 1))               # 1/(x-3)
        s = f + g
        self.assertEqual(s.eval(4), Fraction(1, 2) + Fraction(1, 1))

    def test_equality_uses_domain_fingerprint(self):
        a = rf((2,), (2,))                  # 1 everywhere
        b = RationalFunc.constant(1)
        self.assertEqual(a, b)
        c = rf((0, 2), (0, 1))              # 2 with hole at 0
        d = RationalFunc.constant(2)
        self.assertNotEqual(c, d)
        e = rf((0, 4), (0, 2))              # same hole at 0
        self.assertEqual(c, e)

    def test_steps_record_domain(self):
        f = rf((-1, 0, 1), (-1, 1))
        joined = " ".join(f.steps)
        self.assertIn("定义域", joined)
        self.assertIn("可去奇点", joined)

    def test_zero_denominator_rejected(self):
        with self.assertRaises(ZeroDivisionError):
            rf((1,), (0,))


if __name__ == "__main__":
    unittest.main()

