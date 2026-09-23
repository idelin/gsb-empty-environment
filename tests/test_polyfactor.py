import unittest
from fractions import Fraction

from polyfactor import (
    build_certificate,
    dag_cost,
    expand,
    optimize,
    parse,
    pretty,
    rational_gcd,
)
from polyfactor.parser import ParseError


class TestParser(unittest.TestCase):
    def test_parse_basic(self):
        p = parse("6*x^2*y + 9*x*y^2")
        self.assertEqual(p.vars, ("x", "y"))
        self.assertEqual(p.num_terms, 2)
        self.assertEqual(str(p), "6*x^2*y + 9*x*y^2")

    def test_implicit_mul_and_fraction(self):
        p = parse("2/3x + 4y")
        self.assertEqual(dict(p.terms)[(1, 0)], Fraction(2, 3))
        self.assertEqual(dict(p.terms)[(0, 1)], Fraction(4))

    def test_parens_and_power(self):
        p = parse("(x + 1)^2")
        self.assertEqual(str(p), "x^2 + 2*x + 1")

    def test_negative_exponent_rejected(self):
        with self.assertRaises(ParseError):
            parse("x^-1")

    def test_zero_polynomial_parse(self):
        self.assertTrue(parse("0").is_zero)
        self.assertTrue(parse("x - x").is_zero)


class TestCommonFactor(unittest.TestCase):
    def test_coeff_and_monomial(self):
        p = parse("6*x^2*y + 9*x*y^2")
        g, mono = p.common_factor()
        self.assertEqual(g, Fraction(3))
        self.assertEqual(mono, (1, 1))
        self.assertEqual(str(p.div_term(g, mono)), "2*x + 3*y")

    def test_rational_gcd(self):
        self.assertEqual(rational_gcd([Fraction(1, 2), Fraction(1, 3)]), Fraction(1, 6))
        self.assertEqual(rational_gcd([Fraction(-4, 5), Fraction(2, 3)]), Fraction(2, 15))

    def test_zero_polynomial_common_factor_convention(self):
        g, mono = parse("0").common_factor()
        self.assertEqual((g, mono), (Fraction(1), ()))


class TestOptimize(unittest.TestCase):
    def test_two_term_extraction_exact(self):
        p = parse("6*x^2*y + 9*x*y^2")
        r = optimize(p)
        self.assertTrue(r.complete)
        self.assertEqual(r.cost, 6)  # 平铺需 7 次，提取后 6 次
        self.assertEqual(r.lower_bound, r.upper_bound)
        self.assertEqual(pretty(r.dag, p.vars), "3*x*y*(2*x + 3*y)")

    def test_shared_subexpression_counted_once(self):
        p = parse("2*x + 2*y + 3*x*z + 3*y*z")
        r = optimize(p)
        self.assertTrue(r.complete)
        # (x+y) 或 (2+3z) 共享，只计一次；不共享则为 6
        self.assertEqual(r.cost, 5)

    def test_rational_coefficients_exact(self):
        p = parse("1/2*x + 1/2*y + 1/2*z")
        r = optimize(p)
        self.assertTrue(r.complete)
        self.assertEqual(r.cost, 3)
        self.assertEqual(pretty(r.dag, p.vars), "1/2*(x + y + z)")

    def test_zero_polynomial_special_case(self):
        p = parse("0")
        r = optimize(p)
        self.assertTrue(r.complete)
        self.assertEqual(r.cost, 0)
        self.assertTrue(expand(r.dag, p.vars).is_zero)

    def test_single_term(self):
        p = parse("5*x")
        r = optimize(p)
        self.assertTrue(r.complete)
        self.assertEqual(r.cost, 1)

    def test_budget_interrupt_keeps_bounds(self):
        p = parse("2*x + 2*y + 4*x^2 + 4*x*y + 6*y^2 + 6*x*z + 2*z + 4*y*z")
        r = optimize(p, budget=100)
        self.assertFalse(r.complete)
        self.assertLessEqual(r.lower_bound, r.upper_bound)
        self.assertEqual(r.cost, r.upper_bound)
        self.assertGreater(r.explored, 0)
        # 中断结果乘回仍必须严格等于原式
        self.assertEqual(expand(r.dag, p.vars), p)

    def test_multiply_back_strict_equality(self):
        for text in [
            "6*x^2*y + 9*x*y^2",
            "2*x + 2*y + 3*x*z + 3*y*z",
            "1/2*x + 1/2*y + 1/2*z",
            "7*a*b + 14*a^2*b + 21*a*b*c + 5",
            "x + y + z",
            "3/4*m*n^2 + 9/8*m^2*n",
            "10*u^3*v + 15*u*v^3 + 20*u*v",
        ]:
            p = parse(text)
            r = optimize(p)
            self.assertEqual(expand(r.dag, p.vars), p, text)
            self.assertIsInstance(p.terms[0][1], Fraction)


class TestCertificate(unittest.TestCase):
    def test_steps_verified_and_domain_noted(self):
        p = parse("6*x^2*y + 9*x*y^2")
        r = optimize(p)
        cert = build_certificate(p, r.dag, r.complete)
        self.assertTrue(cert.final_ok)
        self.assertEqual(len(cert.steps), 1)
        self.assertTrue(all(s.verified for s in cert.steps))
        text = cert.render()
        self.assertIn("定义域", text)
        self.assertIn("严格相等", text)

    def test_zero_certificate(self):
        p = parse("0")
        r = optimize(p)
        cert = build_certificate(p, r.dag, r.complete)
        self.assertTrue(cert.zero_case)
        self.assertTrue(cert.final_ok)
        self.assertIn("零多项式", cert.render())

    def test_interrupted_certificate_disclaims_optimality(self):
        p = parse("2*x + 2*y + 4*x^2 + 4*x*y + 6*y^2 + 6*x*z + 2*z + 4*y*z")
        r = optimize(p, budget=100)
        cert = build_certificate(p, r.dag, r.complete)
        self.assertTrue(cert.final_ok)
        self.assertIn("不保证", cert.render())


if __name__ == "__main__":
    unittest.main()
