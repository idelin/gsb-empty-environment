import unittest
from fractions import Fraction

from ratpiece.poly import (
    Poly,
    divmod_poly,
    poly_gcd,
    sign_variations,
    squarefree_part,
    sturm_sequence,
)


class TestPoly(unittest.TestCase):
    def test_arithmetic_exact(self):
        p = Poly((Fraction(1, 2), 1))       # x + 1/2
        q = Poly((Fraction(-1, 2), 1))      # x - 1/2
        self.assertEqual((p + q).coeffs, (Fraction(0), Fraction(2)))
        self.assertEqual((p * q).coeffs,
                         (Fraction(-1, 4), Fraction(0), Fraction(1)))

    def test_eval_and_sign(self):
        p = Poly((-2, 0, 1))                # x^2 - 2
        self.assertEqual(p.eval(Fraction(3, 2)), Fraction(1, 4))
        self.assertEqual(p.sign_at(1), -1)
        self.assertEqual(p.sign_at(2), 1)
        self.assertEqual(p.sign_at(0), -1)

    def test_divmod_exact(self):
        a = Poly((-6, 11, -6, 1))           # (x-1)(x-2)(x-3)
        b = Poly((-1, 1))                   # x - 1
        q, r = divmod_poly(a, b)
        self.assertTrue(r.is_zero())
        self.assertEqual(q, Poly((6, -5, 1)))

    def test_divmod_by_constant(self):
        q, r = divmod_poly(Poly((-2, 0, 1)), Poly.const(2))
        self.assertTrue(r.is_zero())
        self.assertEqual(q, Poly((-1, 0, Fraction(1, 2))))

    def test_gcd(self):
        a = Poly((-1, 0, 1))                # (x-1)(x+1)
        b = Poly((-1, 2, -1))               # (x-1)^2
        self.assertEqual(poly_gcd(a, b), Poly((-1, 1)))

    def test_factor_linear_multiplicity(self):
        p = Poly((2, -5, 4, -1))            # (x-1)^2 (x-2) up to sign
        _, mult = p.factor_linear(1)
        self.assertEqual(mult, 2)
        _, mult = p.factor_linear(2)
        self.assertEqual(mult, 1)
        _, mult = p.factor_linear(3)
        self.assertEqual(mult, 0)

    def test_squarefree_part(self):
        p = Poly((-1, 2, -1))               # (x-1)^2
        self.assertEqual(squarefree_part(p), Poly((-1, 1)))
        q = Poly((4, -4, 1))                # (x-2)^2
        self.assertEqual(squarefree_part(q), Poly((-2, 1)))

    def test_sturm_variations_count_roots(self):
        p = Poly((-6, 11, -6, 1))           # roots 1, 2, 3
        chain = sturm_sequence(p)
        self.assertEqual(sign_variations(chain, 0)
                         - sign_variations(chain, 4), 3)

    def test_to_int_tuple_primitive(self):
        p = Poly((Fraction(1, 2), Fraction(1, 3)))
        self.assertEqual(p.to_int_tuple(), (3, 2))


if __name__ == "__main__":
    unittest.main()

