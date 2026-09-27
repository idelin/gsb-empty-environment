"""Tests for coverage, infinite-weight and low-ESS diagnostics."""

import math
import random
import unittest

from importance_sampling import (
    DiscreteDistribution,
    diagnose_log_weights,
    estimate,
)


def f(x):
    return float(x)


class TestZeroCoverage(unittest.TestCase):
    def test_zero_proposal_mass_where_target_positive_is_unavailable(self):
        target = DiscreteDistribution({0: 0.5, 1: 0.5}, name="target")
        proposal = DiscreteDistribution({0: 1.0}, name="narrow-proposal")
        result = estimate(target, proposal, f, 50, random.Random(0), seed=0)
        self.assertFalse(result.available)
        self.assertIsNone(result.ordinary)
        self.assertIsNone(result.self_normalized)
        codes = [d.code for d in result.errors]
        self.assertIn("ZERO_COVERAGE", codes)
        message = next(d.message for d in result.errors if d.code == "ZERO_COVERAGE")
        self.assertIn("1", message)  # names the uncovered point

    def test_full_coverage_reports_no_error(self):
        target = DiscreteDistribution({0: 0.5, 1: 0.5})
        proposal = DiscreteDistribution({0: 0.9, 1: 0.1})
        result = estimate(target, proposal, f, 50, random.Random(0))
        self.assertTrue(result.available)
        self.assertNotIn("ZERO_COVERAGE", [d.code for d in result.diagnostics])


class TestWeightDiagnostics(unittest.TestCase):
    def test_infinite_weight_is_an_error(self):
        diagnostics = diagnose_log_weights([0.0, math.inf, -1.0])
        errors = [d for d in diagnostics if d.severity == "error"]
        self.assertIn("INFINITE_WEIGHT", [d.code for d in errors])

    def test_all_zero_weights_is_an_error(self):
        diagnostics = diagnose_log_weights([-math.inf, -math.inf])
        self.assertIn("ALL_WEIGHTS_ZERO", [d.code for d in diagnostics])

    def test_nan_weight_is_an_error(self):
        diagnostics = diagnose_log_weights([math.nan])
        self.assertIn("NAN_WEIGHT", [d.code for d in diagnostics])


class TestLowESS(unittest.TestCase):
    def test_mismatched_proposal_triggers_low_ess_warning(self):
        # Target mass sits on 2 of 100 proposal points -> ESS << n.
        target = DiscreteDistribution({0: 0.5, 1: 0.5})
        proposal = DiscreteDistribution({x: 1.0 for x in range(100)})
        result = estimate(target, proposal, f, 400, random.Random(5))
        self.assertTrue(result.available)
        self.assertIn("LOW_ESS", [d.code for d in result.warnings])
        self.assertLess(result.ess, 0.1 * result.n)

    def test_matched_proposal_has_no_low_ess_warning(self):
        target = DiscreteDistribution({0: 0.5, 1: 0.5})
        proposal = DiscreteDistribution({0: 0.5, 1: 0.5})
        result = estimate(target, proposal, f, 200, random.Random(5))
        self.assertEqual(result.warnings, [])
        self.assertAlmostEqual(result.ess, 200.0)


if __name__ == "__main__":
    unittest.main()
