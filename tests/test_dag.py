"""Tests for the parameter expression DAG and interval evaluation."""

import math
import unittest
from decimal import Context, Decimal

from subsample_amp import (
    ApplicabilityError,
    Const,
    ExpM1,
    Interval,
    Log1P,
    Mul,
    Param,
    SubsampleAmp,
    compose_rounds,
    reference_amp,
    subsampled_epsilon,
)

# Heuristic margin (in ulps) absorbing the rounding error of the *float*
# evaluation when comparing it against a rigorous 60-digit interval.
FLOAT_MARGIN_ULP = 8


def float_hull_contains(iv, value, margin=FLOAT_MARGIN_ULP):
    lo_f, hi_f = iv.to_float(margin_ulp=margin)
    return lo_f <= value <= hi_f


class TestDagEvaluation(unittest.TestCase):
    def test_amp_node_contains_reference_and_float(self):
        for eps, q in ((0.5, 0.1), (1.0, 0.5), (50.0, 0.9), (1000.0, 0.01)):
            node = SubsampleAmp(Const(eps), Const(q))
            iv = node.evaluate()
            self.assertTrue(iv.contains(reference_amp(eps, q)), (eps, q, iv))
            float_val = subsampled_epsilon(eps, q)
            self.assertTrue(float_hull_contains(iv, float_val), (eps, q, iv))

    def test_param_binding(self):
        node = SubsampleAmp(Param("eps"), Param("q"))
        iv = node.evaluate({"eps": 1.0, "q": 0.25})
        self.assertTrue(iv.contains(reference_amp(1.0, 0.25)))
        self.assertTrue(float_hull_contains(iv, subsampled_epsilon(1.0, 0.25)))
        with self.assertRaises(KeyError):
            node.evaluate({"eps": 1.0})

    def test_ranged_parameter_interval(self):
        node = SubsampleAmp(Param("eps"), Param("q"))
        iv = node.evaluate({"eps": Interval(0.9, 1.1), "q": Interval(0.2, 0.3)})
        # Monotonicity: function values at the box corners bound the range.
        self.assertLessEqual(iv.lo, reference_amp(0.9, 0.2))
        self.assertGreaterEqual(iv.hi, reference_amp(1.1, 0.3))

    def test_manual_expression_tree(self):
        tree = Log1P(Mul(Const(0.5), ExpM1(Const(1.0))))
        iv = tree.evaluate()
        self.assertTrue(iv.contains(reference_amp(1.0, 0.5)))

    def test_exact_boundaries(self):
        self.assertEqual(SubsampleAmp(Const(3.0), Const(0.0)).evaluate().lo, 0)
        iv = SubsampleAmp(Const(3.0), Const(1.0)).evaluate()
        self.assertTrue(iv.is_point)
        self.assertEqual(iv.lo, Decimal(3.0))


class TestComposition(unittest.TestCase):
    def test_multi_round_composition(self):
        rounds = [(0.5, 0.1)] * 10
        total = compose_rounds(rounds).evaluate()
        oracle = Context(prec=90)
        ref_total = oracle.multiply(reference_amp(0.5, 0.1), Decimal(10))
        self.assertTrue(total.contains(ref_total))
        float_sum = 10 * subsampled_epsilon(0.5, 0.1)
        self.assertTrue(float_hull_contains(total, float_sum, margin=64))
        rel_width = (total.hi - total.lo) / total.hi
        self.assertLess(rel_width, Decimal(10) ** -40)

    def test_many_rounds_no_overflow(self):
        rounds = [(1000.0, 1e-4)] * 100
        total = compose_rounds(rounds).evaluate()
        oracle = Context(prec=90)
        ref_total = oracle.multiply(reference_amp(1000.0, 1e-4), Decimal(100))
        self.assertTrue(total.contains(ref_total))
        lo_f, hi_f = total.to_float()
        self.assertTrue(math.isfinite(lo_f) and math.isfinite(hi_f))

    def test_empty_composition_is_zero(self):
        self.assertEqual(compose_rounds([]).evaluate().lo, 0)

    def test_invalid_round_rejected_at_build(self):
        with self.assertRaises(ApplicabilityError):
            compose_rounds([(0.5, 0.1), (1.0, 1.5)])
        with self.assertRaises(ApplicabilityError):
            compose_rounds([(0.5, 0.1)], model="uniform_without_replacement")
        with self.assertRaises(ApplicabilityError):
            compose_rounds([(0.5, 0.1)], base_delta=1e-6)

    def test_invalid_binding_rejected_at_evaluation(self):
        node = compose_rounds([(Param("eps"), Param("q"))])
        with self.assertRaises(ApplicabilityError):
            node.evaluate({"eps": 1.0, "q": 1.2})
        with self.assertRaises(ApplicabilityError):
            node.evaluate({"eps": 1.0, "q": Interval(0.5, 1.5)})


if __name__ == "__main__":
    unittest.main()
