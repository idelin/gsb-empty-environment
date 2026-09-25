"""Core affine-arithmetic semantics: correlation and independence."""

import unittest

from affine_dag import DAG, IntervalContainsZero
from affine_dag.core import AffineForm, NoiseSymbols, mul, recip


class TestCorrelation(unittest.TestCase):
    def test_x_minus_x_is_exact_zero(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        d = dag.sub(x, x)
        res = dag.evaluate(d)
        self.assertTrue(res.complete)
        self.assertEqual(res.form.terms, {})
        self.assertEqual(res.form.center, 0.0)
        self.assertEqual(res.interval, (0.0, 0.0))  # exact, not [-2, 2]

    def test_independent_vars_same_interval_do_not_cancel(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(-1.0, 1.0, "y")  # same interval, independent symbol
        d = dag.sub(x, y)
        res = dag.evaluate(d)
        self.assertEqual(res.interval, (-2.0, 2.0))
        self.assertEqual(len(res.form.terms), 2)

    def test_shared_symbol_cancels_across_nodes(self):
        # Two references to the same var node share its noise symbol.
        dag = DAG()
        x = dag.var(2.0, 4.0, "x")
        expr = dag.sub(dag.add(x, x), dag.scale(x, 2.0))  # 2x - 2x
        res = dag.evaluate(expr)
        self.assertEqual(res.interval, (0.0, 0.0))

    def test_add_sub_exact(self):
        dag = DAG()
        x = dag.var(0.0, 2.0)
        y = dag.var(-3.0, 1.0)
        s = dag.add(x, y)
        res = dag.evaluate(s)
        self.assertEqual(res.interval, (-3.0, 3.0))


class TestNonlinearRemainders(unittest.TestCase):
    def test_mul_remainder_conservative(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0)
        y = dag.var(-1.0, 1.0)
        m = dag.mul(x, y)
        res = dag.evaluate(m)
        # remainder radius = rad(x)*rad(y) = 1 on a fresh symbol
        self.assertEqual(res.interval, (-1.0, 1.0))
        self.assertTrue(res.complete)

    def test_recip_min_range_bound(self):
        symbols = NoiseSymbols()
        sid = symbols.new(0.5)  # x in [1, 2]
        form = AffineForm(1.5, {sid: 1.0})
        approx, rem = recip(form, symbols)
        symbols2 = NoiseSymbols()
        sid2 = symbols2.new(0.5)
        approx2, rem2 = recip(AffineForm(1.5, {sid2: 1.0}), symbols2)
        # min-range for 1/x on [1,2]: alpha=-1/2, delta = (1.5 - sqrt(2))/2
        self.assertAlmostEqual(rem, (1.5 - 2.0**0.5) / 2.0)
        self.assertEqual(rem, rem2)
        # check the claimed bound actually contains 1/x on a fine grid
        terms = dict(approx.terms)
        sid_r = symbols.new(rem)
        terms[sid_r] = 1.0
        lo, hi = AffineForm(approx.center, terms).interval(symbols)
        for k in range(1001):
            x = 1.0 + k / 1000.0
            self.assertTrue(lo - 1e-12 <= 1.0 / x <= hi + 1e-12)

    def test_recip_interval_containing_zero_raises(self):
        symbols = NoiseSymbols()
        sid = symbols.new(1.0)
        with self.assertRaises(IntervalContainsZero):
            recip(AffineForm(0.5, {sid: 1.0}), symbols)  # interval [-0.5, 1.5]

    def test_recip_negative_interval(self):
        symbols = NoiseSymbols()
        sid = symbols.new(0.5)  # x in [-2, -1]
        approx, rem = recip(AffineForm(-1.5, {sid: 1.0}), symbols)
        terms = dict(approx.terms)
        sid_r = symbols.new(rem)
        terms[sid_r] = 1.0
        lo, hi = AffineForm(approx.center, terms).interval(symbols)
        for k in range(1001):
            x = -2.0 + k / 1000.0
            self.assertTrue(lo - 1e-12 <= 1.0 / x <= hi + 1e-12)


if __name__ == "__main__":
    unittest.main()
