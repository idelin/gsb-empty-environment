"""Tests for mixture proposals and adaptive (multi-round) proposals.

Key invariant under test: every weight's denominator is the density of
the proposal that actually generated the sample (the mixture density for
mixture draws, the per-iteration proposal for adaptive draws).
"""

import random
import unittest

from importance_sampling import (
    AdaptiveIS,
    DiscreteDistribution,
    MixtureProposal,
    effective_sample_size,
    estimate,
    ordinary_is,
    self_normalized_is,
)

TARGET = DiscreteDistribution({0: 0.5, 1: 0.3, 2: 0.2}, name="target")
Q1 = DiscreteDistribution({0: 0.9, 1: 0.08, 2: 0.02}, name="q1")
Q2 = DiscreteDistribution({0: 0.05, 1: 0.15, 2: 0.8}, name="q2")


def f(x):
    return float(x)


class TestMixtureProposal(unittest.TestCase):
    def test_mixture_density_is_weighted_sum(self):
        mix = MixtureProposal([Q1, Q2], weights=[0.25, 0.75], name="mix")
        self.assertAlmostEqual(mix.pdf(0), 0.25 * 0.9 + 0.75 * 0.05)
        self.assertAlmostEqual(mix.pdf(2), 0.25 * 0.02 + 0.75 * 0.8)
        self.assertAlmostEqual(sum(mix.pdf(x) for x in mix.support()), 1.0)

    def test_weights_use_mixture_density_not_component_density(self):
        mix = MixtureProposal([Q1, Q2], weights=[0.5, 0.5], name="mix")
        sampler = AdaptiveIS(TARGET, seed=3)
        sampler.run_iteration(mix, 20, random.Random(11))
        for s in sampler.samples:
            expected = TARGET.log_pdf(s.x) - mix.log_pdf(s.x)
            self.assertAlmostEqual(s.log_weight, expected)
            # The mixture density differs from each component density here,
            # so matching `expected` proves the mixture density was used.
            self.assertNotAlmostEqual(mix.log_pdf(s.x), Q1.log_pdf(s.x))

    def test_mixture_estimate_runs_and_records_metadata(self):
        mix = MixtureProposal([Q1, Q2], weights=[0.5, 0.5], name="mix")
        result = estimate(TARGET, mix, f, 500, random.Random(4), seed=4)
        self.assertTrue(result.available)
        self.assertEqual(result.seed, 4)
        self.assertEqual(result.n, 500)
        self.assertAlmostEqual(result.self_normalized, 0.7, delta=0.05)


class TestAdaptiveIS(unittest.TestCase):
    def test_each_sample_uses_its_own_iteration_proposal(self):
        sampler = AdaptiveIS(TARGET, seed=7)
        sampler.run_iteration(Q1, 3, random.Random(1))
        sampler.run_iteration(Q2, 4, random.Random(2))
        self.assertEqual(sampler.n, 7)
        for s in sampler.samples[:3]:
            self.assertEqual(s.iteration, 0)
            self.assertEqual(s.proposal_id, "q1")
            self.assertAlmostEqual(s.log_weight, TARGET.log_pdf(s.x) - Q1.log_pdf(s.x))
        for s in sampler.samples[3:]:
            self.assertEqual(s.iteration, 1)
            self.assertEqual(s.proposal_id, "q2")
            self.assertAlmostEqual(s.log_weight, TARGET.log_pdf(s.x) - Q2.log_pdf(s.x))

    def test_pooled_estimate_matches_manual_computation(self):
        sampler = AdaptiveIS(TARGET, seed=7)
        sampler.run_iteration(Q1, 30, random.Random(10))
        sampler.run_iteration(Q2, 70, random.Random(20))
        result = sampler.estimate(f)
        logw = [s.log_weight for s in sampler.samples]
        fvals = [f(s.x) for s in sampler.samples]
        self.assertAlmostEqual(result.ordinary, ordinary_is(logw, fvals))
        self.assertAlmostEqual(result.self_normalized, self_normalized_is(logw, fvals))
        self.assertAlmostEqual(result.ess, effective_sample_size(logw))

    def test_zero_coverage_iteration_makes_pooled_estimate_unavailable(self):
        blind = DiscreteDistribution({0: 1.0}, name="blind")
        sampler = AdaptiveIS(TARGET, seed=7)
        sampler.run_iteration(blind, 5, random.Random(3))
        result = sampler.estimate(f)
        self.assertFalse(result.available)
        self.assertIn("ZERO_COVERAGE", [d.code for d in result.errors])

    def test_empty_sampler_reports_no_samples(self):
        result = AdaptiveIS(TARGET, seed=7).estimate(f)
        self.assertFalse(result.available)
        self.assertIn("NO_SAMPLES", [d.code for d in result.errors])


if __name__ == "__main__":
    unittest.main()
