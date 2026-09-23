"""Unittest suite for polyq: exact rational polynomial algebra.

Cross-checks the fast paths (extended_gcd, subresultant PRS) against the
independent naive reference implementation in polyq.reference.
"""
import json
import random
import unittest
from fractions import Fraction

from polyq import (
    poly, trim, deg, lc, monic, add, sub, mul, scale,
    divmod_q, pseudo_divmod, extended_gcd, PRSComputer,
)
from polyq import reference as R


def pmul(*ps):
    out = [Fraction(1)]
    for p in ps:
        out = mul(out, p)
    return out


class TestDivision(unittest.TestCase):
    def test_exact_division(self):
        a = poly([-2, -1, 1])          # x^2 - x - 2 = (x-2)(x+1)
        b = poly([-2, 1])              # x - 2
        q, r = divmod_q(a, b)
        self.assertEqual(q, poly([1, 1]))
        self.assertEqual(r, [])
        R.check_division(a, b, q, r)

    def test_rational_remainder(self):
        a = poly([-2, -1, 1])
        b = poly([-3, 2])              # 2x - 3
        q, r = divmod_q(a, b)
        self.assertEqual(add(mul(q, b), r), a)
        self.assertLess(deg(r), deg(b))
        R.check_division(a, b, q, r)

    def test_remainder_degree_strictly_smaller(self):
        rng = random.Random(7)
        for _ in range(50):
            a = [Fraction(rng.randint(-5, 5), rng.randint(1, 4))
                 for _ in range(rng.randint(0, 8))]
            b = [Fraction(rng.randint(-5, 5), rng.randint(1, 4))
                 for _ in range(rng.randint(1, 6))]
            a, b = trim(a), trim(b)
            if not b:
                continue
            q, r = divmod_q(a, b)
            self.assertLess(deg(r), deg(b))
            R.check_division(a, b, q, r)

    def test_division_by_zero_rejected(self):
        with self.assertRaises(ZeroDivisionError):
            divmod_q(poly([1, 1]), [])
        with self.assertRaises(ZeroDivisionError):
            divmod_q(poly([1, 1]), [0, 0, 0])
        with self.assertRaises(ZeroDivisionError):
            pseudo_divmod(poly([1, 1]), [])
        with self.assertRaises(ZeroDivisionError):
            R.n_divmod(R.n_poly([1]), {})

    def test_dividend_zero(self):
        q, r = divmod_q([], poly([1, 1]))
        self.assertEqual((q, r), ([], []))

    def test_pseudo_division_identity(self):
        rng = random.Random(11)
        for _ in range(50):
            a = trim([Fraction(rng.randint(-4, 4)) for _ in range(rng.randint(1, 7))])
            b = trim([Fraction(rng.randint(-4, 4)) for _ in range(rng.randint(1, 5))])
            if not a or not b or deg(a) < deg(b):
                continue
            q, r, factor = pseudo_divmod(a, b)
            lhs = scale(a, factor)
            self.assertEqual(lhs, add(mul(q, b), r))
            self.assertLess(deg(r), deg(b))
            # factor is a power of lc(b), exponent at most deg(a)-deg(b)+1
            self.assertIn(factor, {lc(b) ** k
                                   for k in range(deg(a) - deg(b) + 2)})


class TestExtendedGcd(unittest.TestCase):
    def test_basic(self):
        a = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]))   # (x-1)^2 (x+2)
        b = pmul(poly([-1, 1]), poly([3, 1]))                  # (x-1)(x+3)
        g, s, t = extended_gcd(a, b)
        self.assertEqual(g, poly([-1, 1]))                     # x - 1, monic
        R.check_gcd(a, b, g, s, t)

    def test_nonmonic_inputs(self):
        a = pmul(poly([-2, 4]), poly([-1, 1]))     # 4(x-1/2)(x-1)
        b = pmul(poly([-6, 3]), poly([-1, 1]))     # 3(x-2)(x-1)
        g, s, t = extended_gcd(a, b)
        self.assertEqual(g, poly([-1, 1]))
        self.assertEqual(lc(g), 1)
        R.check_gcd(a, b, g, s, t)

    def test_multiple_common_factors(self):
        a = pmul(poly([-1, 1]), poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([2, 1]))
        b = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([-5, 1]))
        g, s, t = extended_gcd(a, b)
        self.assertEqual(g, pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1])))
        R.check_gcd(a, b, g, s, t)

    def test_coprime(self):
        g, s, t = extended_gcd(poly([1, 0, 1]), poly([0, 1]))  # x^2+1, x
        self.assertEqual(g, [Fraction(1)])
        R.check_gcd(poly([1, 0, 1]), poly([0, 1]), g, s, t)

    def test_zero_boundaries(self):
        # gcd(0, 0) = 0
        g, s, t = extended_gcd([], [])
        self.assertEqual((g, s, t), ([], [], []))
        R.check_gcd([], [], g, s, t)
        # gcd(a, 0) = monic(a)
        a = poly([2, 4, 6])
        g, s, t = extended_gcd(a, [])
        self.assertEqual(g, monic(a))
        R.check_gcd(a, [], g, s, t)
        # gcd(0, b) = monic(b)
        b = poly([3, 0, -6])
        g, s, t = extended_gcd([], b)
        self.assertEqual(g, monic(b))
        R.check_gcd([], b, g, s, t)
        # zero represented with trailing zeros behaves as zero
        g, s, t = extended_gcd([0, 0, 0], b)
        self.assertEqual(g, monic(b))

    def test_constant_polynomials(self):
        g, s, t = extended_gcd(poly([4]), poly([6]))
        self.assertEqual(g, [Fraction(1)])
        R.check_gcd(poly([4]), poly([6]), g, s, t)

    def test_bezout_exact_random(self):
        rng = random.Random(23)
        for _ in range(60):
            common = trim([Fraction(rng.randint(-3, 3), rng.randint(1, 3))
                           for _ in range(rng.randint(1, 4))])
            u = trim([Fraction(rng.randint(-3, 3), rng.randint(1, 3))
                      for _ in range(rng.randint(1, 5))])
            v = trim([Fraction(rng.randint(-3, 3), rng.randint(1, 3))
                      for _ in range(rng.randint(1, 5))])
            if not common or not u or not v:
                continue
            a, b = mul(common, u), mul(common, v)
            g, s, t = extended_gcd(a, b)
            R.check_gcd(a, b, g, s, t)          # identity + divisibility + monic
            gn, sn, tn = R.n_extended_gcd(R.n_poly(a), R.n_poly(b))
            self.assertEqual(R.n_poly(g), gn)   # agrees with naive reference


class TestPRS(unittest.TestCase):
    def _run_and_verify(self, a, b, budget=None):
        c = PRSComputer(a, b, budget=budget)
        status = c.run()
        return c, status

    def test_matches_extended_gcd(self):
        cases = [
            (pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1])),
             pmul(poly([-1, 1]), poly([3, 1]))),
            (pmul(poly([-1, 1]), poly([-1, 1]), poly([-1, 1]), poly([2, 1])),
             pmul(poly([-1, 1]), poly([-1, 1]), poly([-5, 1]))),
            (poly([1, 0, 1]), poly([0, 1])),
            (poly([2, 4, 6]), []),
            ([], []),
            ([], poly([3, 0, -6])),
            (poly([5]), poly([-2, 7])),
        ]
        for a, b in cases:
            c, status = self._run_and_verify(a, b)
            self.assertEqual(status, "completed")
            g, s, t = extended_gcd(a, b)
            self.assertEqual(c.gcd, g, f"gcd mismatch for {a}, {b}")
            R.check_gcd(a, b, c.gcd, *c.bezout)
            R.check_prs_steps(c.A0, c.B0, c.steps)

    def test_step_certificates_and_degree_descent(self):
        a = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([1, 0, 1]))
        b = pmul(poly([-1, 1]), poly([2, 1]), poly([3, 1]), poly([4, 1]))
        c, _ = self._run_and_verify(a, b)
        self.assertGreater(len(c.steps), 1)
        R.check_prs_steps(c.A0, c.B0, c.steps)   # per-step quotient/remainder + Bezout
        for st in c.steps:
            self.assertLess(deg(st.remainder), deg(st.divisor))
            self.assertIn("domain", st.note)     # every step documents its domain

    def test_nonmonic_and_multiple_factors(self):
        a = pmul(poly([-2, 4]), poly([-1, 1]), poly([-1, 1]), poly([3, 1]))
        b = pmul(poly([-6, 3]), poly([-1, 1]), poly([3, 1]), poly([3, 1]))
        c, status = self._run_and_verify(a, b)
        self.assertEqual(status, "completed")
        self.assertEqual(lc(c.gcd), 1)
        self.assertEqual(c.gcd, pmul(poly([-1, 1]), poly([3, 1])))
        R.check_gcd(a, b, c.gcd, *c.bezout)

    def test_pause_resume(self):
        a = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([1, 0, 1]))
        b = pmul(poly([-1, 1]), poly([3, 1]), poly([-2, 1]))
        ref, _ = self._run_and_verify(a, b)
        c = PRSComputer(a, b)
        statuses = []
        while True:
            st = c.run(max_steps=1)
            statuses.append(st)
            if st == "completed":
                break
            self.assertEqual(st, "paused")
        self.assertIn("paused", statuses)
        self.assertEqual(c.gcd, ref.gcd)
        self.assertEqual(c.bezout, ref.bezout)
        R.check_gcd(a, b, c.gcd, *c.bezout)

    def test_checkpoint_restore(self):
        a = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([1, 0, 1]))
        b = pmul(poly([-1, 1]), poly([3, 1]), poly([-2, 1]), poly([5, 1]))
        ref, _ = self._run_and_verify(a, b)
        c = PRSComputer(a, b)
        c.run(max_steps=1)
        blob = json.dumps(c.checkpoint())          # must be JSON-serialisable
        c2 = PRSComputer.restore(json.loads(blob))
        self.assertEqual(c2.run(), "completed")
        self.assertEqual(c2.gcd, ref.gcd)
        self.assertEqual(c2.bezout, ref.bezout)
        self.assertEqual([st.remainder for st in c2.steps],
                         [st.remainder for st in ref.steps])
        R.check_prs_steps(c2.A0, c2.B0, c2.steps)

    def test_budget_exceeded_stays_unfinished_then_resumes(self):
        a = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([1, 0, 1]),
                 poly([3, 1]))
        b = pmul(poly([-1, 1]), poly([3, 1]), poly([-2, 1]), poly([5, 1]))
        c = PRSComputer(a, b, budget=1)            # absurdly small budget
        status = c.run()
        self.assertEqual(status, "budget_exceeded")
        self.assertIsNone(c.gcd)                   # unfinished: no result
        self.assertEqual(c.run(), "budget_exceeded")  # stays unfinished
        c.budget = 10 ** 6                          # raise budget and resume
        self.assertEqual(c.run(), "completed")
        g, s, t = extended_gcd(a, b)
        self.assertEqual(c.gcd, g)
        R.check_gcd(a, b, c.gcd, *c.bezout)

    def test_random_cross_check(self):
        rng = random.Random(99)
        for _ in range(40):
            common = trim([Fraction(rng.randint(-3, 3), rng.randint(1, 3))
                           for _ in range(rng.randint(1, 4))])
            u = trim([Fraction(rng.randint(-3, 3), rng.randint(1, 3))
                      for _ in range(rng.randint(1, 5))])
            v = trim([Fraction(rng.randint(-3, 3), rng.randint(1, 3))
                      for _ in range(rng.randint(1, 5))])
            if not common or not u or not v:
                continue
            a, b = mul(common, u), mul(common, v)
            c, status = self._run_and_verify(a, b)
            self.assertEqual(status, "completed")
            g, s, t = extended_gcd(a, b)
            self.assertEqual(c.gcd, g)
            R.check_gcd(a, b, c.gcd, *c.bezout)
            R.check_prs_steps(c.A0, c.B0, c.steps)

    def test_growth_control_beats_naive_prs(self):
        # Subresultant scaling must keep coefficients far smaller than an
        # uncontrolled pseudo-remainder sequence on a crafted example.
        a = poly([1, 2, 3, 4, 5, 6, 7])
        b = poly([2, -1, 3, -2, 1, 1])
        c, _ = self._run_and_verify(a, b)
        def maxdigits(p):
            return max((len(str(abs(x.numerator))) + len(str(x.denominator))
                        for x in p), default=0)
        ctrl = max((maxdigits(st.remainder) for st in c.steps), default=0)
        # naive pseudo-remainder sequence without any scaling
        r0, r1 = a, b
        naive = 0
        while r1 and deg(r1) > 0:
            _, prem, _f = pseudo_divmod(r0, r1)
            naive = max(naive, maxdigits(prem))
            r0, r1 = r1, prem
        self.assertLess(ctrl, naive)


if __name__ == "__main__":
    unittest.main()
