import unittest
from fractions import Fraction as F

from interval_arith import (
    Budget, Certificate, Interval, analyze, find_counterexample,
    rational_grid, split_at,
)


class TestDomainSplitting(unittest.TestCase):
    def test_split_at_singularity_excludes_point(self):
        pieces = split_at(Interval(-2, 3), [0])
        self.assertEqual(pieces, [
            Interval(-2, 0, hi_open=True),
            Interval(0, 3, lo_open=True),
        ])
        for p in pieces:
            self.assertFalse(p.contains(0))

    def test_budget_exhaustion_keeps_unanalyzed(self):
        result = analyze(
            lambda iv: iv.reciprocal(),
            Interval(-2, 3),
            singularities=[0],
            budget=Budget(1),
        )
        self.assertEqual(len(result.pieces), 1)
        self.assertEqual(len(result.unanalyzed), 1)
        self.assertEqual(result.unanalyzed[0], Interval(0, 3, lo_open=True))

    def test_full_budget_analyzes_everything(self):
        result = analyze(
            lambda iv: iv.reciprocal(),
            Interval(-2, 3),
            singularities=[0],
            budget=Budget(4),
        )
        self.assertEqual(len(result.pieces), 2)
        self.assertEqual(result.unanalyzed, [])


class TestSampling(unittest.TestCase):
    def test_grid_is_exact_rational(self):
        grid = rational_grid(Interval(-1, 1), 4)
        self.assertEqual(grid, [F(-1), F(-1, 2), F(0), F(1, 2), F(1)])

    def test_counterexample_falsifies_false_bound(self):
        hit = find_counterexample(
            lambda x: x * x, Interval(-1, 1), Interval(0, F(1, 2)), n=8
        )
        self.assertIsNotNone(hit)
        x, y = hit
        self.assertEqual(y, x * x)
        self.assertFalse(Interval(0, F(1, 2)).contains(y))

    def test_no_counterexample_is_not_a_proof(self):
        # No sample violates the true bound -- but this alone proves nothing.
        self.assertIsNone(
            find_counterexample(lambda x: x * x, Interval(-1, 1), Interval(0, 1), n=8)
        )


class TestCertificate(unittest.TestCase):
    def test_verify_true_bound(self):
        cert = Certificate("x^2 on [-1,1] lies in [0,1]")
        ok = cert.verify(lambda iv: iv.square(), Interval(-1, 1), Interval(0, 1))
        self.assertTrue(ok)
        self.assertIn("verified", cert.steps[-1].rule)

    def test_verify_rejects_false_bound(self):
        cert = Certificate("x^2 on [-1,1] lies in [0,1/2]")
        ok = cert.verify(lambda iv: iv.square(), Interval(-1, 1), Interval(0, F(1, 2)))
        self.assertFalse(ok)
        self.assertIn("FAILED", cert.steps[-1].rule)

    def test_render_lists_domains(self):
        cert = Certificate("demo")
        cert.add("step one", domain="[-1, 1]")
        text = cert.render()
        self.assertIn("domain: [-1, 1]", text)


if __name__ == "__main__":
    unittest.main()
