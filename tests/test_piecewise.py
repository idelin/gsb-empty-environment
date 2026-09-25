"""Piecewise-linear functions: kink handling, envelopes, exact segments."""

import unittest

from affine_dag import DAG, PiecewiseLinear, abs_pl, relu_pl
from affine_dag.piecewise import PiecewiseDomainError


class TestKinkCrossing(unittest.TestCase):
    def test_abs_crossing_kink_includes_kink_not_just_endpoints(self):
        dag = DAG()
        x = dag.var(-1.0, 2.0, "x")
        p = dag.piecewise(x, abs_pl())
        res = dag.evaluate(p)
        lo, hi = res.interval
        # Endpoint-only evaluation would give [1, 2]; the kink at 0 forces 0.
        self.assertLessEqual(lo, 0.0)
        self.assertGreaterEqual(hi, 2.0)
        self.assertTrue(res.complete)

    def test_relu_crossing_kink(self):
        dag = DAG()
        x = dag.var(-2.0, 1.0, "x")
        p = dag.piecewise(x, relu_pl())
        res = dag.evaluate(p)
        lo, hi = res.interval
        self.assertLessEqual(lo, 0.0)
        self.assertGreaterEqual(hi, 1.0)

    def test_custom_pl_range_scans_kinks(self):
        # Tent map: peaks at the interior kink x=0 with value 5.
        pl = PiecewiseLinear([-2.0, 0.0, 3.0], [1.0, 5.0, -2.0])
        self.assertEqual(pl.range_on(-2.0, 3.0), (-2.0, 5.0))
        dag = DAG()
        x = dag.var(-2.0, 3.0)
        p = dag.piecewise(x, pl)
        res = dag.evaluate(p)
        self.assertEqual(res.interval, (-2.0, 5.0))

    def test_no_kink_is_exact_and_preserves_correlation(self):
        dag = DAG()
        x = dag.var(1.0, 3.0, "x")  # no kink of abs inside
        p = dag.piecewise(x, abs_pl())
        d = dag.sub(p, x)  # abs(x) - x == 0 for x > 0
        res = dag.evaluate(d)
        self.assertEqual(res.interval, (0.0, 0.0))
        self.assertEqual(res.form.terms, {})

    def test_envelope_drops_correlation_but_stays_sound(self):
        dag = DAG()
        x = dag.var(-1.0, 2.0, "x")
        p = dag.piecewise(x, abs_pl())  # envelope [0, 2]
        d = dag.sub(p, x)  # |x| - x on [-1,2]: true range [0, 3]
        res = dag.evaluate(d)
        lo, hi = res.interval
        self.assertLessEqual(lo, 0.0)
        self.assertGreaterEqual(hi, 3.0)

    def test_out_of_domain_is_explicit_error(self):
        pl = PiecewiseLinear([0.0, 1.0], [0.0, 1.0])  # domain [0, 1]
        dag = DAG()
        x = dag.var(-1.0, 0.5)  # crosses below the domain
        p = dag.piecewise(x, pl)
        res = dag.evaluate(p)
        self.assertFalse(res.complete)
        self.assertTrue(any("outside piecewise domain" in e for e in res.errors))
        with self.assertRaises(PiecewiseDomainError):
            pl.eval(-0.25)

    def test_pl_spec_validation(self):
        with self.assertRaises(ValueError):
            PiecewiseLinear([0.0, 1.0], [0.0])  # size mismatch
        with self.assertRaises(ValueError):
            PiecewiseLinear([1.0, 1.0], [0.0, 1.0])  # not increasing
        with self.assertRaises(ValueError):
            PiecewiseLinear([0.0], [0.0])  # too few points


if __name__ == "__main__":
    unittest.main()
