"""unittest suite for polygcd / prs, validated by the independent naive
reference in reference.py. Run: python3.11 -m unittest -v"""
import random
import unittest
from fractions import Fraction

import reference as ref
from polygcd import (add, as_poly, deg, divmod_poly, lc, monic, mul, scale,
                     sub, xgcd)
from prs import (BudgetExceeded, CoefficientBudget, PRSComputation,
                 pseudo_divmod, subresultant_gcd)


def rand_poly(rng, max_deg=6, lo=-5, hi=5, rational=False):
    d = rng.randint(0, max_deg)
    p = []
    for _ in range(d + 1):
        c = rng.randint(lo, hi)
        p.append(Fraction(c, rng.randint(1, 4)) if rational else Fraction(c))
    return as_poly(p)


def poly_pow(p, n):
    out = [Fraction(1)]
    for _ in range(n):
        out = mul(out, p)
    return out


class TestDivision(unittest.TestCase):
    def test_identity_random(self):
        rng = random.Random(12345)
        for _ in range(200):
            f = rand_poly(rng, rational=rng.random() < 0.5)
            g = rand_poly(rng, max_deg=4)
            if not g:
                continue
            q, r = divmod_poly(f, g)
            self.assertTrue(ref.check_division(f, g, q, r))

    def test_zero_divisor_rejected(self):
        with self.assertRaises(ZeroDivisionError):
            divmod_poly([1, 2, 3], [])
        with self.assertRaises(ZeroDivisionError):
            pseudo_divmod([1, 2, 3], [])

    def test_zero_dividend(self):
        q, r = divmod_poly([], [1, 1])
        self.assertEqual((q, r), ([], []))

    def test_remainder_degree_below_divisor(self):
        f = as_poly([1, 0, 0, 0, 1])       # x^4 + 1
        g = as_poly([1, 1])                # x + 1
        q, r = divmod_poly(f, g)
        self.assertLess(deg(r), deg(g))
        self.assertEqual(add(mul(q, g), r), f)

    def test_pseudo_divmod_identity_random(self):
        rng = random.Random(999)
        for _ in range(200):
            f = rand_poly(rng)
            g = rand_poly(rng, max_deg=5)
            if not g:
                continue
            q, r, mult = pseudo_divmod(f, g)
            self.assertEqual(ref.n_add(ref.n_mul(q, g), r),
                             ref.n_scale(f, mult))
            self.assertTrue(not r or len(r) < len(ref.n_trim(g)))


class TestXGCD(unittest.TestCase):
    def test_bezout_random(self):
        rng = random.Random(2024)
        for _ in range(200):
            f = rand_poly(rng, rational=rng.random() < 0.5)
            g = rand_poly(rng, rational=rng.random() < 0.5)
            d, s, t, _ = xgcd(f, g)
            self.assertTrue(ref.validate_gcd(f, g, d, s, t))

    def test_zero_boundaries(self):
        d, s, t, _ = xgcd([], [])
        self.assertEqual((d, s, t), ([], [], []))
        f = as_poly([2, 4, 2])             # 2(x+1)^2
        d, s, t, _ = xgcd(f, [])
        self.assertEqual(d, monic(f))
        self.assertEqual(ref.n_add(ref.n_mul(s, f), ref.n_mul(t, [])), d)
        d, s, t, _ = xgcd([], f)
        self.assertEqual(d, monic(f))
        self.assertTrue(ref.validate_gcd([], f, d, s, t))

    def test_nonmonic_inputs(self):
        f = as_poly([Fraction(-2), Fraction(4), Fraction(0), Fraction(6)])
        g = as_poly([Fraction(3), Fraction(-9)])
        d, s, t, _ = xgcd(f, g)
        self.assertEqual(d[-1], Fraction(1))
        self.assertTrue(ref.validate_gcd(f, g, d, s, t))

    def test_steps_document_domains(self):
        _, _, _, steps = xgcd([1, 2, 1], [1, 1])
        self.assertTrue(steps)
        for st in steps:
            self.assertTrue("domain" in st or "domains" in st)


class TestPRS(unittest.TestCase):
    def test_multiple_common_factors(self):
        xm1 = as_poly([-1, 1])             # x - 1
        xp2 = as_poly([2, 1])              # x + 2
        f = scale(mul(poly_pow(xm1, 3), poly_pow(xp2, 2)), 6)
        g = scale(mul(poly_pow(xm1, 2), poly_pow(xp2, 3)), 4)
        expected = monic(mul(poly_pow(xm1, 2), poly_pow(xp2, 2)))
        d, s, t = subresultant_gcd(f, g)
        self.assertEqual(d, expected)
        self.assertTrue(ref.validate_gcd(f, g, d, s, t))

    def test_step_identities_descent_and_certificates(self):
        rng = random.Random(77)
        for _ in range(60):
            f = rand_poly(rng, max_deg=7)
            g = rand_poly(rng, max_deg=6)
            comp = PRSComputation(f, g)
            comp.run()
            self.assertEqual(comp.status, PRSComputation.DONE)
            d, s, t = comp.result
            self.assertTrue(ref.validate_prs_steps(f, g, comp.steps, d, s, t))

    def test_matches_independent_naive_gcd_random(self):
        rng = random.Random(31337)
        for _ in range(150):
            f = rand_poly(rng, rational=rng.random() < 0.5)
            g = rand_poly(rng, rational=rng.random() < 0.5)
            d, s, t = subresultant_gcd(f, g)
            self.assertTrue(ref.validate_gcd(f, g, d, s, t))

    def test_growth_control_keeps_integer_coeffs(self):
        # Subresultant PRS of integer polynomials stays in Z[x]:
        # beta factors divide exactly, no denominator explosion.
        rng = random.Random(7)
        for _ in range(40):
            f = rand_poly(rng, max_deg=7, lo=-4, hi=4)
            g = rand_poly(rng, max_deg=6, lo=-4, hi=4)
            if not f or not g:
                continue
            comp = PRSComputation(f, g)
            comp.run()
            for p in comp.remainders:
                for c in p:
                    self.assertEqual(c.denominator, 1,
                                     f"denominator growth in {p}")

    def test_zero_inputs(self):
        comp = PRSComputation([], [])
        self.assertEqual(comp.status, PRSComputation.DONE)
        self.assertEqual(comp.result, ([], [], []))
        f = as_poly([3, 6, 3])
        comp = PRSComputation(f, [])
        self.assertEqual(comp.result[0], monic(f))
        self.assertTrue(ref.validate_gcd(f, [], *comp.result))
        comp = PRSComputation([], f)
        self.assertEqual(comp.result[0], monic(f))
        self.assertTrue(ref.validate_gcd([], f, *comp.result))

    def test_constant_gcd_normalized_to_one(self):
        d, s, t = subresultant_gcd([1, 1], [2, 1])   # coprime linears
        self.assertEqual(d, [Fraction(1)])
        self.assertEqual(ref.n_add(ref.n_mul(s, [1, 1]),
                                   ref.n_mul(t, [2, 1])), d)


class TestBudgetPauseResume(unittest.TestCase):
    def test_budget_exceeded_keeps_unfinished(self):
        f = as_poly([1, 2, 0, 1])          # x^3 + 2x + 1
        g = as_poly([3, -5, 0, 12])        # 12x^3 - 5x + 3
        comp = PRSComputation(f, g, budget=CoefficientBudget(2))
        status = comp.run()
        self.assertEqual(status, PRSComputation.BUDGET_EXCEEDED)
        self.assertIsNone(comp.result)     # unfinished state preserved
        self.assertNotEqual(comp.status, PRSComputation.DONE)
        # Resume with a larger budget completes and validates.
        status = comp.resume(budget=CoefficientBudget(10 ** 6))
        self.assertEqual(status, PRSComputation.DONE)
        self.assertTrue(ref.validate_gcd(f, g, *comp.result))

    def test_subresultant_gcd_raises_on_budget(self):
        f = as_poly([1, 2, 0, 1])
        g = as_poly([3, -5, 0, 12])
        with self.assertRaises(BudgetExceeded):
            subresultant_gcd(f, g, budget=CoefficientBudget(2))

    def test_pause_resume_equivalence(self):
        rng = random.Random(555)
        for _ in range(30):
            f = rand_poly(rng, max_deg=7)
            g = rand_poly(rng, max_deg=6)
            stepped = PRSComputation(f, g)
            while stepped.status != PRSComputation.DONE:
                stepped.run(max_steps=1)
                if stepped.status == PRSComputation.PAUSED:
                    stepped.resume()
            straight = PRSComputation(f, g)
            straight.run()
            self.assertEqual(stepped.result, straight.result)
            self.assertEqual(stepped.remainders, straight.remainders)

    def test_snapshot_restore(self):
        rng = random.Random(4242)
        for _ in range(20):
            f = rand_poly(rng, max_deg=7)
            g = rand_poly(rng, max_deg=6)
            comp = PRSComputation(f, g)
            comp.run(max_steps=2)
            snap = comp.snapshot()
            restored = PRSComputation.from_snapshot(snap)
            restored.resume()
            fresh = PRSComputation(f, g)
            fresh.run()
            self.assertEqual(restored.status, PRSComputation.DONE)
            self.assertEqual(restored.result, fresh.result)

    def test_snapshot_is_json_serializable(self):
        import json
        comp = PRSComputation([1, 2, 0, 1], [3, -5, 0, 12])
        comp.run(max_steps=1)
        json.dumps(comp.snapshot())


if __name__ == "__main__":
    unittest.main()
