"""unittest suite for the sturmiso package (Python 3.11, stdlib only)."""
import unittest
from fractions import Fraction as F

from sturmiso import (
    Interval,
    isolate,
    normalize,
    resume_isolation,
    revise_polynomial,
    squarefree_factorization,
    verify_isolation,
    verify_revision,
)
from sturmiso.poly import (
    derivative,
    divmod_poly,
    evaluate,
    gcd_poly,
    is_zero,
    mul,
    primitive_integer,
    sub,
    to_string,
)
from sturmiso.sturm import count_distinct_roots, sturm_chain
from sturmiso.verify import (
    verify_count_certificate,
    verify_squarefree_factorization,
)


def P(*coeffs):
    return normalize(coeffs)


class TestPoly(unittest.TestCase):
    def test_arithmetic(self):
        p = P(1, 2, 1)
        q = P(1, 1)
        quo, rem = divmod_poly(p, q)
        self.assertEqual(quo, q)
        self.assertTrue(is_zero(rem))
        self.assertEqual(derivative(P(1, 0, 1)), P(0, 2))
        self.assertEqual(evaluate(P(-2, 0, 1), F(2)), F(2))

    def test_gcd_and_sub(self):
        a = mul(P(-1, 1), P(-1, 1))
        b = mul(P(-1, 1), P(-2, 1))
        self.assertEqual(gcd_poly(a, b), P(-1, 1))
        self.assertTrue(is_zero(sub(a, a)))

    def test_primitive_integer(self):
        self.assertEqual(
            primitive_integer(P(F(1, 2), F(-1, 3))), (-3, 2)
        )
        self.assertEqual(primitive_integer(P(0, 0, -4)), (0, 0, 1))

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            normalize([0.5, 1.0])

    def test_to_string(self):
        self.assertEqual(to_string(P(-2, 0, 1)), "x^2 - 2")
        self.assertEqual(to_string(P(0)), "0")


class TestSquareFree(unittest.TestCase):
    def test_repeated_factors(self):
        f = P(0, 0, -1, 3, -3, 1)  # x^2 (x-1)^3
        facs = squarefree_factorization(f)
        by_index = {fac.index: fac for fac in facs}
        self.assertEqual(set(by_index), {2, 3})
        self.assertEqual(by_index[2].poly, P(0, 1))
        self.assertEqual(by_index[3].poly, P(-1, 1))
        self.assertEqual(by_index[2].multiplicity, 2)
        self.assertTrue(verify_squarefree_factorization(f, facs))

    def test_pure_square(self):
        facs = squarefree_factorization(P(0, 0, 1))  # x^2
        self.assertEqual(len(facs), 1)
        self.assertEqual(facs[0].index, 2)
        self.assertEqual(facs[0].poly, P(0, 1))

    def test_constant_and_zero(self):
        self.assertEqual(squarefree_factorization(P(5)), [])
        self.assertEqual(squarefree_factorization(P(0)), [])

    def test_product_is_f_up_to_unit(self):
        f = P(2, -4, 2)  # 2(x-1)^2
        facs = squarefree_factorization(f)
        self.assertTrue(verify_squarefree_factorization(f, facs))


class TestSturmCounting(unittest.TestCase):
    def setUp(self):
        self.g = P(0, -1, 1)  # x(x-1), roots exactly 0 and 1
        self.chain = sturm_chain(self.g)

    def test_endpoint_root_rules(self):
        cases = [
            (True, True, 2),
            (False, False, 0),
            (True, False, 1),
            (False, True, 1),
        ]
        for lo_c, hi_c, expected in cases:
            with self.subTest(lo_c=lo_c, hi_c=hi_c):
                self.assertEqual(
                    count_distinct_roots(
                        self.chain, F(0), F(1), lo_c, hi_c
                    ),
                    expected,
                )

    def test_point_interval(self):
        self.assertEqual(
            count_distinct_roots(self.chain, F(0), F(0), True, True), 1
        )
        self.assertEqual(
            count_distinct_roots(self.chain, F(2), F(2), True, True), 0
        )

    def test_unbounded(self):
        self.assertEqual(
            count_distinct_roots(self.chain, None, "+inf", False, False), 2
        )
        self.assertEqual(
            count_distinct_roots(self.chain, None, F(0), False, True), 1
        )

    def test_no_real_roots(self):
        chain = sturm_chain(P(1, 0, 1))  # x^2+1
        self.assertEqual(
            count_distinct_roots(chain, None, "+inf", False, False), 0
        )

    def test_not_squarefree_rejected(self):
        with self.assertRaises(ValueError):
            sturm_chain(P(0, 0, 1))  # x^2 is not square-free


class TestIsolation(unittest.TestCase):
    def test_two_irrational_roots(self):
        res = isolate(P(-2, 0, 1))
        self.assertTrue(res.complete)
        self.assertEqual(len(res.isolated), 2)
        self.assertTrue(all(r.multiplicity == 1 for r in res.isolated))
        self.assertTrue(verify_isolation(res))

    def test_repeated_root_multiplicity_listed(self):
        res = isolate(P(0, 0, 1))  # x^2: one distinct root 0, multiplicity 2
        self.assertEqual(len(res.isolated), 1)
        root = res.isolated[0]
        self.assertEqual(root.multiplicity, 2)
        self.assertTrue(root.interval.contains(F(0)))
        self.assertEqual([f.index for f in root.factors], [2])
        self.assertTrue(verify_isolation(res))

    def test_no_real_roots_returns_empty(self):
        res = isolate(P(1, 0, 1))
        self.assertEqual(res.isolated, [])
        self.assertEqual(res.pending, [])
        self.assertTrue(verify_isolation(res))

    def test_zero_and_constant_polynomial(self):
        for f in (P(0), P(7)):
            res = isolate(f)
            self.assertEqual(res.isolated, [])
            self.assertEqual(res.pending, [])
            self.assertTrue(verify_isolation(res))

    def test_exact_rational_roots_become_points(self):
        res = isolate(P(2, -3, 1))  # (x-1)(x-2)
        # every rational root is certified inside exactly one isolation
        # interval; roots hit exactly by a bisection midpoint become
        # point intervals {r}
        self.assertEqual(len(res.isolated), 2)
        for root_value in (F(1), F(2)):
            hits = [
                r for r in res.isolated
                if r.interval.contains(root_value)
            ]
            self.assertEqual(len(hits), 1)
        self.assertTrue(any(r.is_point for r in res.isolated))
        self.assertTrue(verify_isolation(res))

    def test_seed_with_endpoint_roots(self):
        res = isolate(
            P(0, -1, 1),
            seeds=[Interval(F(0), F(1), True, True)],
            seeds_cover_all_roots=True,
        )
        points = sorted(r.point for r in res.isolated if r.is_point)
        self.assertEqual(points, [F(0), F(1)])
        self.assertTrue(verify_isolation(res))

    def test_intervals_pairwise_disjoint(self):
        res = isolate(P(24, -50, 35, -10, 1))
        ivs = [r.interval for r in res.isolated]
        for i in range(len(ivs)):
            for j in range(i + 1, len(ivs)):
                self.assertIsNone(ivs[i].intersect(ivs[j]))

    def test_budget_interruption_and_resume(self):
        f = P(24, -50, 35, -10, 1)
        part = isolate(f, budget=1)
        self.assertFalse(part.complete)
        self.assertEqual(part.total_certified_roots, 4)
        self.assertTrue(verify_isolation(part))
        for p in part.pending:
            self.assertGreaterEqual(p.count, 2)
            self.assertTrue(
                verify_count_certificate(part.radical, p.count_certificate)
            )
        done = resume_isolation(part)
        self.assertTrue(done.complete)
        self.assertEqual(len(done.isolated), 4)
        self.assertTrue(verify_isolation(done))

    def test_budget_zero(self):
        res = isolate(P(-2, 0, 1), budget=0)
        self.assertEqual(res.isolated, [])
        self.assertEqual(len(res.pending), 1)
        self.assertEqual(res.pending[0].count, 2)
        self.assertTrue(verify_isolation(res))

    def test_certificate_tampering_detected(self):
        res = isolate(P(-2, 0, 1))
        cert = res.isolated[0].count_certificate
        bad = type(cert)(
            interval=cert.interval,
            chain_degrees=cert.chain_degrees,
            lower_endpoint=cert.lower_endpoint,
            upper_endpoint=cert.upper_endpoint,
            count=cert.count + 1,
            rule=cert.rule,
        )
        self.assertFalse(verify_count_certificate(res.radical, bad))


class TestRevision(unittest.TestCase):
    def test_exact_rational_match(self):
        old = isolate(P(1, -2, 1))  # (x-1)^2
        rep = revise_polynomial(old, P(2, -3, 1))  # (x-1)(x-2)
        kinds = sorted(g.kind for g in rep.groups)
        self.assertEqual(kinds, ["new_only", "one_to_one"])
        self.assertEqual([r.new_count for r in rep.reused], [1])
        self.assertTrue(verify_revision(rep))

    def test_split_is_grouped_not_guessed(self):
        old = isolate(P(-2, 0, 1))  # x^2-2
        new = P(F(9999, 10000), -2, 1)  # roots 1 +/- 1/100
        rep = revise_polynomial(old, new)
        splits = [g for g in rep.groups if g.kind == "split"]
        self.assertEqual(len(splits), 1)
        self.assertEqual(len(splits[0].old_ids), 1)
        self.assertEqual(len(splits[0].new_ids), 2)
        one_to_one = [g for g in rep.groups if g.kind == "one_to_one"]
        self.assertEqual(one_to_one, [])
        self.assertTrue(verify_revision(rep))

    def test_merge_is_grouped_not_guessed(self):
        old = isolate(P(F(9999, 10000), -2, 1))
        rep = revise_polynomial(old, P(-2, 0, 1))
        merges = [g for g in rep.groups if g.kind == "merge"]
        self.assertEqual(len(merges), 1)
        self.assertEqual(len(merges[0].old_ids), 2)
        self.assertEqual(len(merges[0].new_ids), 1)
        self.assertTrue(verify_revision(rep))

    def test_identity_revision(self):
        old = isolate(P(-2, 0, 1))
        rep = revise_polynomial(old, P(-2, 0, 1))
        kinds = sorted(g.kind for g in rep.groups)
        self.assertEqual(kinds, ["one_to_one", "one_to_one"])
        self.assertTrue(verify_revision(rep))

    def test_root_appears_and_vanishes(self):
        old = isolate(P(1, 0, 1))  # no real roots
        rep = revise_polynomial(old, P(-2, 0, 1))
        kinds = sorted(g.kind for g in rep.groups)
        self.assertEqual(kinds, ["new_only", "new_only"])
        self.assertTrue(verify_revision(rep))

    def test_revision_budget_interruption(self):
        old = isolate(P(-2, 0, 1))
        rep = revise_polynomial(old, P(24, -50, 35, -10, 1), budget=0)
        self.assertFalse(rep.new_result.complete)
        self.assertTrue(rep.new_result.pending)
        self.assertTrue(verify_revision(rep))


if __name__ == "__main__":
    unittest.main()
