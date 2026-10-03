import unittest

from dualtag import Tag
from dualtag import dual as D
from dualtag.dual import Dual
from dualtag.errors import DomainError, NonDifferentiableError


class TestDualArithmetic(unittest.TestCase):
    def setUp(self):
        self.t = Tag("test")

    def seed(self, x, dirs=(1.0,)):
        return Dual(float(x), {self.t: tuple(dirs)})

    def test_add_mul_chain(self):
        x = self.seed(2.0)
        y = x * x + 3.0 * x
        self.assertAlmostEqual(D.deep_primal(y), 10.0)
        self.assertAlmostEqual(y.partials[self.t][0], 2 * 2.0 + 3.0)

    def test_div_and_pow(self):
        x = self.seed(4.0)
        y = x ** 2 / x
        self.assertAlmostEqual(D.deep_primal(y), 4.0)
        self.assertAlmostEqual(y.partials[self.t][0], 1.0)

    def test_multi_direction_slots(self):
        x = self.seed(3.0, (1.0, 2.0))
        y = x * x
        self.assertEqual(y.partials[self.t], (6.0, 12.0))

    def test_constant_folding_returns_float(self):
        y = 2.0 * 3.0 + 1.0
        self.assertIsInstance(y, float)

    def test_float_conversion_guard(self):
        with self.assertRaises(TypeError):
            float(self.seed(1.0))
        self.assertEqual(float(Dual(2.5)), 2.5)

    def test_nested_primal_levels(self):
        t2 = Tag("inner")
        x = Dual(Dual(2.0, {self.t: (1.0,)}), {t2: (1.0,)})
        y = x * x
        inner_tangent = y.partials[t2][0]
        self.assertIsInstance(inner_tangent, Dual)
        self.assertAlmostEqual(inner_tangent.partials[self.t][0], 2.0)


class TestNonSmooth(unittest.TestCase):
    def setUp(self):
        self.t = Tag("kink")

    def seed(self, x):
        return Dual(float(x), {self.t: (1.0,)})

    def test_abs(self):
        self.assertAlmostEqual(D.deep_primal(D.dual_abs(self.seed(-2.0))), 2.0)
        self.assertEqual(D.dual_abs(self.seed(-2.0)).partials[self.t], (-1.0,))
        with self.assertRaises(NonDifferentiableError):
            D.dual_abs(self.seed(0.0))
        self.assertEqual(D.dual_abs(0.0), 0.0)  # constant input: fine

    def test_min_max_ties(self):
        with self.assertRaises(NonDifferentiableError):
            D.dual_min(self.seed(1.0), 1.0)
        with self.assertRaises(NonDifferentiableError):
            D.dual_max(self.seed(1.0), 1.0)
        self.assertEqual(D.dual_min(self.seed(0.5), 1.0).partials[self.t], (1.0,))

    def test_sqrt_at_zero(self):
        with self.assertRaises(NonDifferentiableError):
            D.sqrt(self.seed(0.0))
        self.assertEqual(D.sqrt(0.0), 0.0)

    def test_domain_errors(self):
        with self.assertRaises(DomainError):
            D.log(self.seed(-1.0))
        with self.assertRaises(DomainError):
            D.sqrt(self.seed(-0.5))
        with self.assertRaises(DomainError):
            D.pow_(self.seed(-2.0), self.seed(0.5))


if __name__ == "__main__":
    unittest.main()
