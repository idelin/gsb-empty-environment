import unittest
from dataclasses import replace
from fractions import Fraction

from sturm_iso import (isolate, revise_isolation, revise_polynomial,
                       verify_certificate, verify_isolation)
from sturm_iso.polynomials import (derivative, divmod_poly, evaluate, gcd,
                                   mul, poly, squarefree_factorization)


def P(*coeffs):
    return poly(coeffs)


class PolynomialTests(unittest.TestCase):
    def test_divmod_exact(self):
        q, r = divmod_poly(P(-1, 0, 1), P(-1, 1))
        self.assertEqual(q, P(1, 1))
        self.assertEqual(r, P())

    def test_gcd_monic(self):
        self.assertEqual(gcd(P(-1, 0, 1), P(1, -2, 1)), P(-1, 1))

    def test_derivative(self):
        self.assertEqual(derivative(P(5, 0, 2, -1)), P(0, 4, -3))

    def test_yun_multiplicities(self):
        p = mul(mul(P(-1, 1), P(-1, 1)),
                mul(P(-2, 1), mul(P(-2, 1), P(-2, 1))))
        factors = dict(squarefree_factorization(p))
        self.assertEqual(factors, {P(-1, 1): 2, P(-2, 1): 3})

    def test_revise_polynomial_helper(self):
        p = revise_polynomial(P(1, 2, 3), {0: 5, 3: Fraction(1, 2)})
        self.assertEqual(p, P(5, 2, 3, Fraction(1, 2)))


class IsolationTests(unittest.TestCase):
    def test_x_squared_single_root_multiplicity_two(self):
        # x^2 has only the root 0; multiplicity 2 is listed separately.
        result = isolate(P(0, 0, 1))
        self.assertTrue(result.completed)
        self.assertEqual(result.total_distinct_roots, 1)
        self.assertEqual(len(result.roots), 1)
        cert = result.roots[0]
        self.assertEqual(cert.multiplicity, 2)
        self.assertEqual(cert.squarefree_factor, P(0, 1))
        self.assertLess(cert.interval.lo, 0)
        self.assertGreaterEqual(cert.interval.hi, 0)
        self.assertEqual(verify_isolation(result), [])

    def test_no_real_roots_returns_empty(self):
        result = isolate(P(1, 0, 1))  # x^2 + 1
        self.assertEqual(result.roots, [])
        self.assertEqual(result.pending, [])
        self.assertTrue(result.completed)
        self.assertEqual(result.total_distinct_roots, 0)
        self.assertEqual(verify_isolation(result), [])

    def test_three_simple_roots(self):
        p = mul(mul(P(-1, 1), P(-2, 1)), P(-3, 1))
        result = isolate(p)
        self.assertEqual(len(result.roots), 3)
        self.assertTrue(all(c.multiplicity == 1 for c in result.roots))
        self.assertEqual(verify_isolation(result), [])

    def test_endpoint_exact_roots_consistent_counting(self):
        p = mul(mul(P(0, 1), P(-1, 1)), P(-2, 1))  # x(x-1)(x-2)
        result = isolate(p)
        kinds = sorted(c.kind for c in result.roots)
        self.assertEqual(kinds, ["interval", "point", "point"])
        points = sorted(c.point for c in result.roots if c.kind == "point")
        self.assertEqual(points, [Fraction(0), Fraction(2)])
        iv_cert = next(c for c in result.roots if c.kind == "interval")
        self.assertTrue(iv_cert.interval.hi_excluded)
        # hi is an exact root, excluded from the open interval (0, 2).
        self.assertEqual(evaluate(result.squarefree_part,
                                  iv_cert.interval.hi), 0)
        self.assertLess(iv_cert.interval.lo, 1)
        self.assertGreater(iv_cert.interval.hi, 1)
        self.assertEqual(iv_cert.root_count, 1)
        self.assertEqual(verify_isolation(result), [])

    def test_multiplicities_listed_separately(self):
        p = mul(mul(P(-1, 1), P(-1, 1)), P(-2, 1))  # (x-1)^2 (x-2)
        result = isolate(p)
        self.assertEqual(result.total_distinct_roots, 2)
        self.assertEqual(sorted(c.multiplicity for c in result.roots), [1, 2])
        double = next(c for c in result.roots if c.multiplicity == 2)
        self.assertEqual(double.squarefree_factor, P(-1, 1))
        self.assertEqual(verify_isolation(result), [])

    def test_budget_interruption_returns_pending(self):
        p = mul(mul(P(-1, 1), P(-2, 1)), mul(P(-3, 1), P(-4, 1)))
        partial = isolate(p, max_steps=1)
        self.assertFalse(partial.completed)
        self.assertTrue(partial.pending)
        accounted = len(partial.roots) + sum(
            x.root_count for x in partial.pending)
        self.assertEqual(accounted, partial.total_distinct_roots)
        self.assertEqual(verify_isolation(partial), [])
        full = isolate(p)
        self.assertTrue(full.completed)
        self.assertEqual(len(full.roots), 4)
        self.assertEqual(verify_isolation(full), [])

    def test_constant_and_zero_polynomial(self):
        self.assertEqual(isolate(P(5)).roots, [])
        self.assertTrue(isolate(P(5)).completed)
        with self.assertRaises(ValueError):
            isolate(P())

    def test_verifier_detects_tampering(self):
        result = isolate(mul(P(-1, 1), P(-2, 1)))
        cert = next(c for c in result.roots if c.kind == "interval")
        bad_count = replace(cert, variations_lo=cert.variations_lo + 1)
        self.assertTrue(verify_certificate(result.polynomial, bad_count))
        bad_mult = replace(cert, multiplicity=cert.multiplicity + 1)
        self.assertTrue(verify_certificate(result.polynomial, bad_mult))
        good = verify_certificate(result.polynomial, cert)
        self.assertEqual(good, [])


class RevisionTests(unittest.TestCase):
    def test_identity_preserved_by_containment(self):
        old = isolate(P(-2, 0, 1))  # x^2 - 2
        new_p = revise_polynomial(old.polynomial, {0: Fraction(-199, 100)})
        rev = revise_isolation(old, new_p)
        self.assertEqual(rev.reused_intervals, 2)
        relations = sorted(link.relation for link in rev.links)
        self.assertEqual(relations, ["preserved", "preserved"])
        old_ids = sorted(c.root_id for c in old.roots)
        new_ids = sorted(c.root_id for c in rev.isolation.roots)
        self.assertEqual(old_ids, new_ids)
        self.assertEqual(verify_isolation(rev.isolation), [])

    def test_multiple_root_split_not_guessed(self):
        base = P(-2, 0, 1)
        old = isolate(mul(base, base))  # (x^2-2)^2: two double roots
        new_p = revise_polynomial(old.polynomial, {0: Fraction(399, 100)})
        rev = revise_isolation(old, new_p)
        splits = [link for link in rev.links if link.relation == "split"]
        self.assertEqual(len(splits), 2)
        for link in splits:
            self.assertEqual(len(link.old_ids), 1)
            # set-valued descendants: no unique 1-1 identity is claimed
            self.assertEqual(len(link.new_ids), 2)
        self.assertFalse(any(l.relation == "preserved" for l in rev.links))
        self.assertEqual(len(rev.isolation.roots), 4)
        self.assertEqual(verify_isolation(rev.isolation), [])

    def test_merge_reported_as_candidate_only(self):
        old = isolate(mul(P(-1, 1), P(-2, 1)))  # roots 1, 2
        new_p = mul(P(-3, 1), P(-3, 1))         # double root 3
        rev = revise_isolation(old, new_p)
        self.assertFalse(any(l.relation == "preserved" for l in rev.links))
        merges = [l for l in rev.links if l.relation == "merge_candidate"]
        self.assertEqual(len(merges), 1)
        self.assertEqual(len(merges[0].old_ids), 2)
        self.assertEqual(len(merges[0].new_ids), 1)
        self.assertEqual(verify_isolation(rev.isolation), [])

    def test_point_root_preserved_exactly(self):
        old = isolate(mul(P(0, 1), P(-2, 1)))  # x(x-2): point 0, root 2
        new_p = mul(P(0, 1), P(-3, 1))         # x(x-3)
        rev = revise_isolation(old, new_p)
        preserved = [l for l in rev.links if l.relation == "preserved"]
        self.assertTrue(preserved)
        # point root 0 is preserved exactly; root 3 lands exactly on the
        # old interval's right endpoint and is extracted as a point root
        # by the exact endpoint rule.
        point_certs = sorted(
            c.point for c in rev.isolation.roots if c.kind == "point")
        self.assertEqual(point_certs, [Fraction(0), Fraction(3)])
        zero = next(c for c in rev.isolation.roots
                    if c.kind == "point" and c.point == 0)
        self.assertTrue(any(zero.root_id in l.old_ids
                            for l in preserved))
        self.assertEqual(verify_isolation(rev.isolation), [])

    def test_revision_budget_interruption(self):
        base = P(-2, 0, 1)
        old = isolate(mul(base, base))
        new_p = revise_polynomial(old.polynomial, {0: Fraction(399, 100)})
        rev = revise_isolation(old, new_p, max_steps=0)
        self.assertFalse(rev.isolation.completed)
        self.assertTrue(rev.isolation.pending)
        self.assertEqual(verify_isolation(rev.isolation), [])

    def test_revision_to_polynomial_without_real_roots(self):
        old = isolate(P(-2, 0, 1))       # x^2 - 2
        rev = revise_isolation(old, P(2, 0, 1))  # x^2 + 2
        self.assertEqual(rev.isolation.roots, [])
        self.assertTrue(all(l.relation == "vanished" for l in rev.links))
        self.assertEqual(verify_isolation(rev.isolation), [])


if __name__ == "__main__":
    unittest.main()
