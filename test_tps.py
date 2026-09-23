import unittest
from fractions import Fraction

from tps import TPS


class TestConstruction(unittest.TestCase):
    def test_coeffs_are_exact_fractions(self):
        f = TPS([1, Fraction(1, 3), 0.5], order=3)
        self.assertEqual(f.coeffs, (Fraction(1), Fraction(1, 3), Fraction(1, 2)))
        for c in f.coeffs:
            self.assertIsInstance(c, Fraction)

    def test_padding_and_truncation_to_order(self):
        self.assertEqual(TPS([1, 2], order=4).coeffs,
                         (Fraction(1), Fraction(2), Fraction(0), Fraction(0)))
        self.assertEqual(TPS([1, 2, 3, 4], order=2).coeffs,
                         (Fraction(1), Fraction(2)))

    def test_default_order_is_coeff_length(self):
        self.assertEqual(TPS([1, 2, 3]).order, 3)

    def test_nonpositive_order_rejected(self):
        with self.assertRaises(ValueError):
            TPS([1], order=0)


class TestAddSub(unittest.TestCase):
    def test_add_exact(self):
        f = TPS([Fraction(1, 3), 1], order=3)
        g = TPS([Fraction(1, 6), -1], order=3)
        self.assertEqual((f + g).coeffs,
                         (Fraction(1, 2), Fraction(0), Fraction(0)))
        self.assertEqual((f + g).order, 3)

    def test_sub_exact(self):
        f = TPS([Fraction(1, 3)], order=2)
        g = TPS([Fraction(1, 2)], order=2)
        self.assertEqual((f - g).coeffs, (Fraction(-1, 6), Fraction(0)))

    def test_order_mismatch_raises(self):
        with self.assertRaises(ValueError):
            TPS([1], order=2) + TPS([1], order=3)


class TestMul(unittest.TestCase):
    def test_mul_keeps_only_specified_order(self):
        f = TPS([1, 1, 1, 1], order=4)
        g = TPS([1, 1, 1, 1], order=4)
        h = f.mul(g, order=2)
        self.assertEqual(h.coeffs, (Fraction(1), Fraction(2)))
        self.assertEqual(h.order, 2)

    def test_mul_default_order_is_min(self):
        f = TPS([1, 2, 3], order=3)
        g = TPS([1, 1], order=2)
        h = f * g
        self.assertEqual(h.order, 2)
        self.assertEqual(h.coeffs, (Fraction(1), Fraction(3)))

    def test_mul_truncates_high_terms(self):
        # (1 + x + x^2)(1 + x + x^2) = 1 + 2x + 3x^2 + 2x^3 + x^4
        f = TPS([1, 1, 1], order=3)
        h = f * f
        self.assertEqual(h.coeffs, (Fraction(1), Fraction(2), Fraction(3)))

    def test_mul_exact_rationals(self):
        f = TPS([Fraction(1, 3), Fraction(1, 7)], order=2)
        g = TPS([Fraction(3, 2), Fraction(2, 5)], order=2)
        h = f * g
        self.assertEqual(h[0], Fraction(1, 2))
        self.assertEqual(h[1], Fraction(1, 3) * Fraction(2, 5)
                         + Fraction(1, 7) * Fraction(3, 2))


class TestInverse(unittest.TestCase):
    def test_geometric_series(self):
        # 1/(1 - x) = 1 + x + x^2 + ... (mod x^5)
        f = TPS([1] + [-1] + [0] * 3, order=5)
        self.assertEqual(f.inverse().coeffs, (Fraction(1),) * 5)

    def test_inverse_of_inverse_constant(self):
        # 1/2 的逆是 2
        f = TPS([2, 4, 8], order=3)
        g = f.inverse()
        self.assertEqual(g[0], Fraction(1, 2))

    def test_f_times_inverse_is_one_within_truncation(self):
        f = TPS([Fraction(2, 3), Fraction(-1, 5), Fraction(7, 2), 4], order=4)
        g = f.inverse()
        product = f.mul(g, order=4)
        self.assertEqual(product, TPS.one(4))

    def test_inverse_order_propagation(self):
        f = TPS([1, 1, 1], order=3)
        self.assertEqual(f.inverse().order, 3)
        self.assertEqual(f.inverse(order=6).order, 6)

    def test_zero_constant_raises_not_faked(self):
        f = TPS([0, 1, 2], order=3)
        with self.assertRaises(ValueError):
            f.inverse()
        # 零级数同样不允许
        with self.assertRaises(ValueError):
            TPS.zero(3).inverse()


class TestCompose(unittest.TestCase):
    def test_basic_composition(self):
        # f = 1 + x + x^2, g = x + x^2
        # f∘g = 1 + (x+x^2) + (x+x^2)^2 = 1 + x + 2x^2 (mod x^3)
        f = TPS([1, 1, 1], order=3)
        g = TPS([0, 1, 1], order=3)
        self.assertEqual(f.compose(g).coeffs,
                         (Fraction(1), Fraction(1), Fraction(2)))

    def test_composition_exact_rationals(self):
        # f = 1 + x + x^2/2, g = x/3
        # f∘g = 1 + x/3 + x^2/18 (mod x^3)
        f = TPS([1, 1, Fraction(1, 2)], order=3)
        g = TPS([0, Fraction(1, 3)], order=3)
        self.assertEqual(f.compose(g).coeffs,
                         (Fraction(1), Fraction(1, 3), Fraction(1, 18)))

    def test_identity_inner_returns_outer(self):
        f = TPS([3, Fraction(-1, 2), 5], order=3)
        x = TPS([0, 1], order=3)
        self.assertEqual(f.compose(x), f)

    def test_nonzero_constant_inner_raises(self):
        f = TPS([1, 1, 1], order=3)
        g = TPS([1, 1], order=3)  # 常数项 1，非法
        with self.assertRaises(ValueError):
            f.compose(g)

    def test_compose_order_propagation(self):
        f = TPS([1, 1, 1, 1], order=4)
        g = TPS([0, 1, 1], order=3)
        self.assertEqual(f.compose(g).order, 3)          # min(4, 3)
        self.assertEqual(f.compose(g, order=2).order, 2) # 显式指定

    def test_compose_matches_mul_inverse_consistency(self):
        # exp 截断级数与 log 截断级数互为逆（在复合意义下）：
        # E(x) = 1 + x + x^2/2 + x^3/6,  L(x) = x - x^2/2 + x^3/3
        # E(L(x)) = 1 + x (mod x^4)
        e = TPS([1, 1, Fraction(1, 2), Fraction(1, 6)], order=4)
        log1p = TPS([0, 1, Fraction(-1, 2), Fraction(1, 3)], order=4)
        self.assertEqual(e.compose(log1p),
                         TPS([1, 1, 0, 0], order=4))


if __name__ == "__main__":
    unittest.main()
