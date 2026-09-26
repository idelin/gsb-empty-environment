import unittest
from fractions import Fraction

from rational_pieces.poly import Poly


class TestPoly(unittest.TestCase):
    def test_exact_arithmetic(self):
        p = Poly([Fraction(1, 2), 0, Fraction(-1, 3)])
        q = Poly([1, Fraction(1, 6)])
        s = p + q
        self.assertEqual(s.eval(0), Fraction(3, 2))
        self.assertEqual((p * q).eval(2), p.eval(2) * q.eval(2))

    def test_divmod_exact(self):
        p = Poly([-1, 0, 1])       # x^2 - 1
        q = Poly([-1, 1])          # x - 1
        quo, rem = p.divmod(q)
        self.assertEqual(quo, Poly([1, 1]))
        self.assertTrue(rem.is_zero())

    def test_gcd_monic(self):
        a = Poly([-1, 0, 1])       # (x-1)(x+1)
        b = Poly([1, -2, 1])       # (x-1)^2
        self.assertEqual(a.gcd(b), Poly([-1, 1]))

    def test_derivative_and_squarefree(self):
        p = Poly([-1, 0, 0, 1])    # x^3 - 1... use (x-1)^2 = x^2-2x+1
        sq = Poly([1, -2, 1]).squarefree_part()
        self.assertEqual(sq, Poly([-1, 1]))

    def test_deflate_multiplicity(self):
        p = Poly([-8, 12, -6, 1])  # (x-2)^3
        m, r = p.deflate(2)
        self.assertEqual(m, 3)
        self.assertEqual(r, Poly.one())

    def test_eval_fraction_exact(self):
        p = Poly([0, 0, 1])        # x^2
        self.assertEqual(p.eval(Fraction(1, 3)), Fraction(1, 9))


if __name__ == '__main__':
    unittest.main()
