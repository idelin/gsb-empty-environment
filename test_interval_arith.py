"""unittest suite for interval_arith (Python 3.11, standard library only)."""

import unittest
from fractions import Fraction as F

from interval_arith import (
    Interval, Domain, divide, reciprocal, POS_INF, NEG_INF,
    AffineForm, Certificate, Step, certify_range,
    Var, Const, Add, Sub, Mul, Square, var, const,
    split_domain, analyze, rational_samples, find_counterexample,
)


class TestIntervalCore(unittest.TestCase):
    def test_add_sub_exact(self):
        a = Interval(F(1, 3), F(1, 2))
        b = Interval(F(1, 6), F(1, 4))
        self.assertEqual(a + b, Interval(F(1, 2), F(3, 4)))
        self.assertEqual(a - b, Interval(F(1, 12), F(1, 3)))

    def test_mul_exact(self):
        self.assertEqual(Interval(-2, 3) * Interval(F(1, 2), 4),
                         Interval(-8, 12))

    def test_exact_rational_no_float_drift(self):
        third = Interval.point(F(1, 3))
        self.assertEqual(third * Interval.point(3), Interval.point(1))

    def test_square_mixed_sign_is_exact(self):
        # [-1, 1]^2 must be [0, 1], not [-1, 1].
        self.assertEqual(Interval(-1, 1).square(), Interval(0, 1))
        self.assertEqual(Interval(-3, 2).square(), Interval(0, 9))
        self.assertEqual(Interval(1, 2).square(), Interval(1, 4))

    def test_naive_mul_is_conservative_for_self_product(self):
        # Plain interval multiplication treats the two factors as
        # independent: x * x on [-1, 1] gives [-1, 1].  Sound, but
        # conservative -- the dependency is lost (see affine tests).
        x = Interval(-1, 1)
        naive = x * x
        self.assertEqual(naive, Interval(-1, 1))
        self.assertTrue(x.square().is_subset(naive))

    def test_monotone_function(self):
        cube = Interval(-2, 3).apply_monotone(lambda v: v ** 3)
        self.assertEqual(cube, Interval(-8, 27))
        dec = Interval(1, 4).apply_monotone(lambda v: 1 / v, increasing=False)
        self.assertEqual(dec, Interval(F(1, 4), 1))

    def test_from_float_rounds_outward(self):
        x = 0.1
        iv = Interval.from_float(x)
        self.assertTrue(iv.contains(x))
        self.assertLess(iv.lo, x)
        self.assertGreater(iv.hi, x)

    def test_from_decimal_is_exact(self):
        iv = Interval.from_decimal("0.1")
        self.assertEqual(iv, Interval.point(F(1, 10)))


class TestDivision(unittest.TestCase):
    def test_straddling_zero_gives_disconnected_domain(self):
        res = divide(Interval.point(1), Interval(-1, 2))
        self.assertFalse(res.undefined)
        self.assertEqual(len(res.domain.parts), 2)
        self.assertEqual(res.domain.parts[0], Interval(NEG_INF, -1))
        self.assertEqual(res.domain.parts[1], Interval(F(1, 2), POS_INF))
        self.assertTrue(res.domain.contains(-2))
        self.assertTrue(res.domain.contains(F(3, 4)))
        self.assertFalse(res.domain.contains(0))

    def test_divisor_exactly_zero_is_undefined(self):
        res = divide(Interval(1, 2), Interval(0, 0))
        self.assertTrue(res.undefined)
        self.assertIn("undefined", res.note)

    def test_divisor_touching_zero_gives_single_ray(self):
        res = divide(Interval.point(1), Interval(0, 2))
        self.assertEqual(res.domain.parts, (Interval(F(1, 2), POS_INF),))

    def test_ordinary_division(self):
        res = divide(Interval(1, 2), Interval(2, 4))
        self.assertEqual(res.domain.parts, (Interval(F(1, 4), 1),))

    def test_numerator_straddling_zero_fills_reals(self):
        res = divide(Interval(-2, 4), Interval(-1, 2))
        self.assertEqual(res.domain.parts, (Interval(NEG_INF, POS_INF),))


class TestAffine(unittest.TestCase):
    def test_self_subtraction_cancels_exactly(self):
        env = {"x": Interval(-1, 1)}
        expr = Sub(var("x"), var("x"))
        self.assertEqual(expr.affine(env).range(), Interval(0, 0))
        # Plain interval arithmetic cannot see the cancellation:
        self.assertEqual(expr.interval(env), Interval(-2, 2))

    def test_self_product_on_minus1_1_is_exact(self):
        # Affine x*x on [-1, 1]: e^2 in [0, 1] exactly -> range [0, 1].
        env = {"x": Interval(-1, 1)}
        expr = Mul(var("x"), var("x"))
        self.assertEqual(expr.affine(env).range(), Interval(0, 1))
        # Dependency-unaware interval multiplication is conservative:
        self.assertEqual(expr.interval(env), Interval(-1, 1))

    def test_square_node(self):
        env = {"x": Interval(-1, 1)}
        self.assertEqual(Square(var("x")).affine(env).range(), Interval(0, 1))

    def test_affine_expression_with_remainder(self):
        # x*(x+1) on [0, 1]: true range [0, 2]; affine gives a
        # conservative enclosure that must contain it.
        env = {"x": Interval(0, 1)}
        expr = Mul(var("x"), Add(var("x"), const(1)))
        r = expr.affine(env).range()
        self.assertTrue(Interval(0, 2).is_subset(r))
        self.assertLess(r.lo, 1)  # strictly tighter than naive on the left
        self.assertTrue(r.is_subset(Interval(-1, 3)))

    def test_unbounded_interval_rejected(self):
        with self.assertRaises(ValueError):
            AffineForm.from_interval("e", Interval(0, POS_INF))


class TestCertificate(unittest.TestCase):
    def _certified(self, claimed):
        env = {"x": Interval(-1, 1)}
        cert = Certificate()
        expr = Mul(var("x"), var("x"))
        form = expr.affine(env, cert)
        r = certify_range(form, claimed, cert)
        return cert, r

    def test_certificate_verifies_correct_bound(self):
        cert, r = self._certified(Interval(0, 1))
        self.assertEqual(r, Interval(0, 1))
        self.assertTrue(cert.verify())

    def test_certificate_rejects_wrong_bound(self):
        cert, _ = self._certified(Interval(F(1, 2), 1))  # too narrow
        self.assertFalse(cert.verify())

    def test_certificate_detects_tampering(self):
        cert, _ = self._certified(Interval(0, 1))
        cert.steps[-1].inputs = (Interval(0, 1), Interval(F(9, 10), 1))
        self.assertFalse(cert.verify())

    def test_explain_lists_domain_notes(self):
        cert, _ = self._certified(Interval(0, 1))
        text = cert.explain()
        self.assertIn("affine_mul", text)
        self.assertIn("[-1, 1]", text)


class TestSplitting(unittest.TestCase):
    def test_split_at_singularity(self):
        pieces = split_domain(Interval(-2, 3), [F(0)])
        self.assertEqual(pieces, [Interval(-2, 0), Interval(0, 3)])

    def test_analyze_reciprocal_pieces(self):
        res = analyze(lambda p: divide(Interval.point(1), p),
                      Interval(-2, 3), [F(0)])
        self.assertEqual(len(res.analyzed), 2)
        self.assertEqual(res.unanalyzed, [])
        left, right = res.analyzed
        self.assertEqual(left[1].domain.parts, (Interval(NEG_INF, F(-1, 2)),))
        self.assertEqual(right[1].domain.parts, (Interval(F(1, 3), POS_INF),))

    def test_budget_exhaustion_keeps_unanalyzed(self):
        res = analyze(lambda p: divide(Interval.point(1), p),
                      Interval(-2, 3), [F(0)], budget=1)
        self.assertEqual(len(res.analyzed), 1)
        self.assertEqual(res.unanalyzed, [Interval(0, 3)])


class TestSampling(unittest.TestCase):
    def test_counterexample_found_for_wrong_bound(self):
        # Claiming x^2 on [-1, 1] stays in [-1/2, 1/2] is false; sampling
        # finds an exact rational counterexample.
        bad = Interval(F(-1, 2), F(1, 2))
        ce = find_counterexample(lambda x: x * x, Interval(-1, 1), bad)
        self.assertIsNotNone(ce)
        self.assertFalse(bad.contains(ce * ce))

    def test_no_counterexample_is_not_a_proof(self):
        # No counterexample among samples -- but this alone proves nothing;
        # inclusion is established only by the certificate (TestCertificate).
        good = Interval(0, 1)
        ce = find_counterexample(lambda x: x * x, Interval(-1, 1), good)
        self.assertIsNone(ce)

    def test_samples_are_exact_rationals(self):
        pts = rational_samples(Interval(0, 1), 4)
        self.assertEqual(pts, [F(0), F(1, 4), F(1, 2), F(3, 4), F(1)])
        self.assertTrue(all(isinstance(p, F) for p in pts))


if __name__ == "__main__":
    unittest.main(verbosity=2)
