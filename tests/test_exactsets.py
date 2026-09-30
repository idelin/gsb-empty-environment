import unittest
from copy import deepcopy
from fractions import Fraction

from exactsets import polys as P
from exactsets import (
    AlgebraicNumber,
    Budget,
    Rat,
    RealSet,
    VerificationError,
    evaluator,
    isolate_all,
    solve,
    verify,
)

X2M2 = [-2, 0, 1]  # x^2 - 2
SQRT2 = lambda: AlgebraicNumber(X2M2, Fraction(1), Fraction(2))
NEG_SQRT2 = lambda: AlgebraicNumber(X2M2, Fraction(-2), Fraction(-1))


def grid(lo, hi, den):
    return [Fraction(k, den) for k in range(lo * den, hi * den + 1)]


class TestCoreSemantics(unittest.TestCase):
    def test_self_cancelling_ratio(self):
        # (x^2-2)/(x^2-2) >= 0  ==  R \ {-sqrt2, +sqrt2}
        e = Rat(X2M2, X2M2) >= 0
        sol = solve(e)
        self.assertEqual(sol.status, "complete")
        s = sol.set
        self.assertEqual(len(s.components), 3)
        for c in s.components:
            self.assertFalse(c.lo_closed)
            self.assertFalse(c.hi_closed)
        # endpoints are exactly +-sqrt(2), excluded
        self.assertFalse(s.contains_point(SQRT2()))
        self.assertFalse(s.contains_point(NEG_SQRT2()))
        self.assertTrue(s.contains_point(Fraction(0)))
        self.assertTrue(s.contains_point(Fraction(-2)))
        self.assertTrue(s.contains_point(Fraction(2)))
        # domain exclusions recorded as a reviewable step
        self.assertEqual(len(sol.steps.critical_roots), 2)
        self.assertEqual(sol.steps.domain_excluded, [True, True])
        self.assertTrue(verify(sol.certificate))
        self.assertEqual(evaluator.cross_check(e, s, grid(-3, 3, 3)), [])

    def test_square_le_zero_isolated_points(self):
        # (x^2-2)^2 <= 0  ==  {-sqrt2, +sqrt2}
        e = Rat(P.mul(X2M2, X2M2)) <= 0
        sol = solve(e)
        s = sol.set
        self.assertEqual(len(s.components), 2)
        for c in s.components:
            self.assertTrue(c.lo_closed and c.hi_closed)
            self.assertIs(c.lo, c.hi)  # isolated point component
            self.assertIsInstance(c.lo, AlgebraicNumber)
        self.assertTrue(s.contains_point(SQRT2()))
        self.assertTrue(s.contains_point(NEG_SQRT2()))
        self.assertFalse(s.contains_point(Fraction(0)))
        self.assertFalse(s.contains_point(Fraction(7, 5)))  # close to sqrt2
        # multiplicity 2: no sign change, roots kept as isolated points
        self.assertEqual(sol.steps.multiplicities, [[2], [2]])
        self.assertTrue(verify(sol.certificate))
        self.assertEqual(evaluator.cross_check(e, s, grid(-3, 3, 2)), [])

    def test_not_does_not_restore_domain_point(self):
        # NOT(1/(x-1) > 0)  ==  (-oo, 1); 1 stays excluded
        e = ~(Rat([1], [-1, 1]) > 0)
        sol = solve(e)
        s = sol.set
        self.assertEqual(len(s.components), 1)
        c = s.components[0]
        self.assertIsNone(c.lo)
        self.assertEqual(c.hi, Fraction(1))
        self.assertFalse(c.hi_closed)
        self.assertFalse(s.contains_point(Fraction(1)))
        self.assertTrue(s.contains_point(Fraction(0)))
        self.assertFalse(s.contains_point(Fraction(2)))
        self.assertTrue(verify(sol.certificate))
        self.assertEqual(evaluator.cross_check(e, s, grid(-3, 4, 2)), [])


class TestRootHandling(unittest.TestCase):
    def test_shared_roots_across_polynomials(self):
        # p1 = (x^2-2)(x-1) >= 0 AND p2 = (x^2-2)(x-3) <= 0
        #    == {-sqrt2} u [sqrt2, 3]; +-sqrt2 shared via gcd evidence
        p1 = P.mul(X2M2, [-1, 1])
        p2 = P.mul(X2M2, [-3, 1])
        e = (Rat(p1) >= 0) & (Rat(p2) <= 0)
        sol = solve(e)
        s = sol.set
        # exactly 4 distinct critical roots: -sqrt2, 1, sqrt2, 3
        self.assertEqual(len(sol.steps.critical_roots), 4)
        self.assertEqual(len(s.components), 2)
        c0, c1 = s.components
        self.assertIs(c0.lo, c0.hi)  # isolated point
        self.assertEqual(
            AlgebraicNumber.compare(c0.lo, NEG_SQRT2()), 0
        )
        self.assertEqual(
            AlgebraicNumber.compare(c1.lo, SQRT2()), 0
        )
        self.assertTrue(c1.lo_closed)
        self.assertEqual(c1.hi, Fraction(3))
        self.assertTrue(c1.hi_closed)
        self.assertTrue(verify(sol.certificate))
        self.assertEqual(evaluator.cross_check(e, s, grid(-3, 4, 5)), [])

    def test_extremely_close_distinct_roots(self):
        # (x-1)(x-(1+2^-40)) <= 0  ==  [1, 1+2^-40]
        delta = Fraction(1, 2**40)
        p = P.mul([-1, 1], [-(1 + delta), 1])
        e = Rat(p) <= 0
        sol = solve(e)
        s = sol.set
        self.assertEqual(len(s.components), 1)
        c = s.components[0]
        self.assertEqual(c.lo, Fraction(1))
        self.assertTrue(c.lo_closed)
        self.assertEqual(c.hi, 1 + delta)
        self.assertTrue(c.hi_closed)
        self.assertTrue(s.contains_point(1 + delta / 2))
        self.assertFalse(s.contains_point(1 + 2 * delta))
        self.assertFalse(s.contains_point(1 - delta))
        self.assertTrue(verify(sol.certificate))

    def test_zero_polynomial_denominator(self):
        e = Rat([1], P.sub([0, 1], [0, 1])) > 0  # denominator x - x == 0
        sol = solve(e)
        self.assertEqual(sol.status, "complete")
        self.assertTrue(sol.set.is_empty())
        # zero denominator anywhere empties the whole common domain
        e2 = (Rat([1], [0]) > 0) | (Rat([0, 1]) > 0)
        self.assertTrue(solve(e2).set.is_empty())

    def test_rational_root_endpoints(self):
        p = P.mul([Fraction(-1, 3), 1], [-2, 1])  # (x - 1/3)(x - 2)
        e = Rat(p) <= 0
        s = solve(e).set
        c, = s.components
        self.assertEqual(c.lo, Fraction(1, 3))
        self.assertEqual(c.hi, Fraction(2))
        self.assertTrue(c.lo_closed and c.hi_closed)

    def test_multiplicity_controls_sign_change(self):
        # (x-1)^2 (x-2) >= 0  ==  {1} u [2, oo)
        p = P.mul(P.mul([-1, 1], [-1, 1]), [-2, 1])
        e = Rat(p) >= 0
        sol = solve(e)
        s = sol.set
        self.assertEqual(sol.steps.multiplicities, [[2], [1]])
        c0, c1 = s.components
        self.assertEqual(c0.lo, Fraction(1))
        self.assertIs(c0.lo, c0.hi)
        self.assertEqual(c1.lo, Fraction(2))
        self.assertTrue(c1.lo_closed)
        self.assertIsNone(c1.hi)
        self.assertTrue(verify(sol.certificate))
        self.assertEqual(evaluator.cross_check(e, s, grid(-2, 4, 2)), [])

    def test_not_equal_operator(self):
        e = Rat([-1, 1]).ne(0)  # x != 1
        sol = solve(e)
        s = sol.set
        self.assertEqual(len(s.components), 2)
        self.assertFalse(s.contains_point(Fraction(1)))
        self.assertTrue(s.contains_point(Fraction(0)))
        self.assertTrue(verify(sol.certificate))

    def test_equality_operator(self):
        e = Rat(X2M2).eq(0)  # x^2 - 2 = 0
        sol = solve(e)
        s = sol.set
        self.assertEqual(len(s.components), 2)
        self.assertTrue(s.contains_point(SQRT2()))
        self.assertFalse(s.contains_point(Fraction(1)))
        self.assertTrue(verify(sol.certificate))


class TestSetOpsAndAffine(unittest.TestCase):
    def test_union_intersection_difference(self):
        s1 = solve(Rat(X2M2) <= 0).set        # [-sqrt2, sqrt2]
        s2 = solve(Rat([-1, 0, 1]) >= 0).set  # (-oo,-1] u [1,oo)
        inter = s1.intersection(s2)           # [-sqrt2,-1] u [1,sqrt2]
        union = s1.union(s2)
        diff = s1.difference(s2)              # (-1, 1)
        e_i = (Rat(X2M2) <= 0) & (Rat([-1, 0, 1]) >= 0)
        e_u = (Rat(X2M2) <= 0) | (Rat([-1, 0, 1]) >= 0)
        e_d = (Rat(X2M2) <= 0) & ~(Rat([-1, 0, 1]) >= 0)
        self.assertEqual(inter, solve(e_i).set)
        self.assertEqual(union, solve(e_u).set)
        self.assertEqual(diff, solve(e_d).set)
        for s, e in ((inter, e_i), (union, e_u), (diff, e_d)):
            self.assertEqual(evaluator.cross_check(e, s, grid(-3, 3, 2)), [])

    def test_affine_substitution(self):
        s = solve(Rat(X2M2) <= 0).set  # [-sqrt2, sqrt2]
        # x = 2t + 1
        t = s.affine(Fraction(2), Fraction(1))
        q = P.compose(X2M2, [1, 2])  # (2t+1)^2 - 2
        self.assertEqual(t, solve(Rat(q) <= 0).set)
        # negative slope flips order; this set is symmetric
        self.assertEqual(s.affine(Fraction(-1), Fraction(0)), s)
        # fractional slope and shift
        t3 = s.affine(Fraction(1, 2), Fraction(-3))
        q3 = P.compose(X2M2, [-3, Fraction(1, 2)])
        self.assertEqual(t3, solve(Rat(q3) <= 0).set)
        # canonical endpoint form is stable
        self.assertIn("RootOf(x^2 - 2, #0", s.canonical_str())
        self.assertIn("RootOf(x^2 - 2, #1", s.canonical_str())

    def test_affine_of_isolated_points(self):
        s = solve(Rat(P.mul(X2M2, X2M2)) <= 0).set  # {+-sqrt2}
        t = s.affine(Fraction(1), Fraction(-1))     # shift by 1: x = t - 1
        q = P.compose(P.mul(X2M2, X2M2), [-1, 1])
        self.assertEqual(t, solve(Rat(q) <= 0).set)


class TestBudgets(unittest.TestCase):
    def test_refinement_budget_partial(self):
        e = Rat(X2M2) >= 0
        sol = solve(e, Budget(max_refine=0))
        self.assertEqual(sol.status, "partial")
        self.assertIsNone(sol.set)
        self.assertTrue(sol.pending)  # undecided isolating regions reported
        self.assertIsNone(sol.certificate)

    def test_bit_budget_partial(self):
        e = Rat(X2M2) >= 0
        sol = solve(e, Budget(max_bits=1))
        self.assertEqual(sol.status, "partial")
        self.assertIsNone(sol.set)

    def test_generous_budget_completes(self):
        e = Rat(X2M2) >= 0
        sol = solve(e, Budget(max_bits=1000, max_refine=1000))
        self.assertEqual(sol.status, "complete")
        self.assertTrue(verify(sol.certificate))


class TestCertificateVerifier(unittest.TestCase):
    def setUp(self):
        self.expr = (Rat(X2M2) <= 0) | (Rat([-1, 1]) > 0)
        self.sol = solve(self.expr)
        self.assertTrue(verify(self.sol.certificate))

    def test_tampered_cell_sign_rejected(self):
        cert = deepcopy(self.sol.certificate)
        cert.cell_signs[0][0] = -cert.cell_signs[0][0]
        with self.assertRaises(VerificationError):
            verify(cert)

    def test_tampered_isolating_interval_rejected(self):
        cert = deepcopy(self.sol.certificate)
        # widen the first algebraic interval to swallow both roots of x^2-2
        cert.roots[0].lo = Fraction(-2)
        cert.roots[0].hi = Fraction(2)
        with self.assertRaises(VerificationError):
            verify(cert)

    def test_tampered_domain_exclusion_rejected(self):
        e = Rat(X2M2, X2M2) >= 0
        cert = deepcopy(solve(e).certificate)
        cert.domain_excluded[0] = not cert.domain_excluded[0]
        with self.assertRaises(VerificationError):
            verify(cert)

    def test_tampered_multiplicity_rejected(self):
        e = Rat(P.mul(X2M2, X2M2)) <= 0
        cert = deepcopy(solve(e).certificate)
        cert.multiplicities[0][0] = 1
        with self.assertRaises(VerificationError):
            verify(cert)

    def test_tampered_claimed_set_rejected(self):
        cert = deepcopy(self.sol.certificate)
        cert.claimed = RealSet(cert.claimed.components[:-1])
        with self.assertRaises(VerificationError):
            verify(cert)

    def test_tampered_atom_poly_rejected(self):
        cert = deepcopy(self.sol.certificate)
        cert.atoms[0].poly = [Fraction(1)]
        with self.assertRaises(VerificationError):
            verify(cert)


class TestLowLevel(unittest.TestCase):
    def test_sturm_root_counting(self):
        self.assertEqual(P.count_roots(P.norm(X2M2), P.NINF, P.INF), 2)
        self.assertEqual(P.count_roots(P.norm(X2M2), Fraction(0), P.INF), 1)
        p = P.norm([2, 0, 1])  # x^2 + 2: no real roots
        self.assertEqual(P.count_roots(p, P.NINF, P.INF), 0)

    def test_isolation_and_canonical_form(self):
        roots = isolate_all(X2M2)
        self.assertEqual(len(roots), 2)
        cr = roots[1].canonical()
        self.assertEqual(tuple(cr.poly), (-2, 0, 1))
        self.assertEqual(cr.index, 1)
        self.assertLess(cr.lo, cr.hi)

    def test_squarefree_factorization(self):
        p = P.mul(P.mul([-1, 1], [-1, 1]), [-2, 1])
        facts = dict((P.deg(f), m) for f, m in P.squarefree_factorization(p))
        self.assertEqual(sorted(m for _, m in P.squarefree_factorization(p)), [1, 2])

    def test_algebraic_equality_across_polys(self):
        # sqrt(2) as root of x^2-2 and as root of (x^2-2)(x-5)
        a = AlgebraicNumber(X2M2, Fraction(1), Fraction(2))
        b = AlgebraicNumber(P.mul(X2M2, [-5, 1]), Fraction(0), Fraction(3, 2))
        self.assertTrue(a.equals(b))
        c = AlgebraicNumber(P.mul(X2M2, [-5, 1]), Fraction(2), Fraction(6))
        self.assertFalse(a.equals(c))  # that's the root 5
        self.assertLess(AlgebraicNumber.compare(a, c), 0)

    def test_direct_evaluator_domain(self):
        e = Rat([1], [-1, 1]) > 0
        self.assertFalse(evaluator.evaluate(e, 1))  # excluded from domain
        self.assertTrue(evaluator.evaluate(e, 2))
        self.assertFalse(evaluator.evaluate(e, 0))


if __name__ == "__main__":
    unittest.main()
