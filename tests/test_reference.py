import math
import unittest
from decimal import Decimal

from amplification import invert, reference, stable


def rel_err(a: float, b: Decimal) -> float:
    b_float = float(b)
    if b_float == 0.0:
        return abs(a)
    return abs(a - b_float) / abs(b_float)


class TestSeriesKernels(unittest.TestCase):
    def test_ln2_series(self):
        got = float(reference._ln2(60))
        self.assertAlmostEqual(got, math.log(2.0), places=15)

    def test_ln_series(self):
        for z in (0.5, 1.0, 2.0, 7.25, 1e6):
            got = float(reference._ln_series(Decimal(str(z)), 60))
            self.assertAlmostEqual(got, math.log(z), places=12)

    def test_expm1_series(self):
        for x in (1e-6, 0.1, 1.0, 20.0):
            got = float(reference._expm1_series(Decimal(str(x)), 60))
            self.assertAlmostEqual(got, math.expm1(x), places=12)


class TestReferenceVsFloat(unittest.TestCase):
    def check_agreement(self, q, eps, tol=1e-12):
        ref = reference.reference_eps_prime(q, eps)
        got = stable.eps_prime(q, eps)
        self.assertLessEqual(
            rel_err(got, ref),
            tol,
            f"q={q}, eps={eps}: float={got!r} ref={ref}",
        )

    def test_moderate_regime(self):
        for q, eps in ((0.1, 1.0), (0.5, 2.0), (0.9, 5.0), (0.01, 0.5)):
            self.check_agreement(q, eps)

    def test_tiny_q_regime(self):
        self.check_agreement(1e-12, 1.0)
        self.check_agreement(1e-8, 3.0)

    def test_tiny_eps_regime(self):
        self.check_agreement(0.5, 1e-8)
        self.check_agreement(0.9, 1e-4)

    def test_huge_eps_regime(self):
        # Decimal reference handles e^1000 exactly; float path must agree.
        self.check_agreement(1e-3, 700.0, tol=1e-10)
        self.check_agreement(1e-3, 1000.0, tol=1e-10)
        self.check_agreement(1e-300, 800.0, tol=1e-8)

    def test_boundary_q_one_is_exact(self):
        ref = reference.reference_eps_prime(1.0, 2.5)
        self.assertEqual(ref, Decimal("2.5"))
        self.assertEqual(stable.eps_prime(1.0, 2.5), 2.5)

    def test_boundary_q_zero_is_exact(self):
        self.assertEqual(reference.reference_eps_prime(0.0, 5.0), Decimal(0))

    def test_reference_max_q_matches_float(self):
        for eps, budget in ((2.0, 0.5), (10.0, 0.01), (1000.0, 5.0)):
            ref = reference.reference_max_q(eps, budget)
            got = invert._analytic_q(eps, budget)
            self.assertLessEqual(rel_err(got, ref), 1e-12)

    def test_inversion_bracket_encloses_reference(self):
        # The certified float bracket must enclose the high-precision truth.
        for eps, budget in ((3.0, 0.2), (50.0, 1.0), (1000.0, 690.0)):
            res = invert.max_q_for_budget(eps, budget)
            ref = float(reference.reference_max_q(eps, budget))
            self.assertLessEqual(res.q_lo, ref)
            self.assertLessEqual(ref, res.q_hi)


class TestReferenceRejection(unittest.TestCase):
    def test_reference_rejects_out_of_domain(self):
        with self.assertRaises(ValueError):
            reference.reference_eps_prime(1.5, 1.0)
        with self.assertRaises(ValueError):
            reference.reference_eps_prime(0.5, -1.0)
        with self.assertRaises(ValueError):
            reference.reference_max_q(-1.0, 0.5)


if __name__ == "__main__":
    unittest.main()
