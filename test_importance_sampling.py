"""Tests for importance_sampling.

Tests are labeled [EXACT] (analytic identities that must hold to machine
precision) or [EMPIRICAL] (Monte Carlo properties validated by simulation
with fixed seeds and statistical tolerances).
"""

import math
import random
import tempfile
import unittest
from pathlib import Path

from importance_sampling import (
    ALL_WEIGHTS_ZERO,
    EMPTY_SAMPLE,
    INFINITE_WEIGHT,
    LOW_ESS,
    ZERO_COVERAGE,
    AdaptiveIS,
    DiscreteDistribution,
    MixtureProposal,
    check_discrete_coverage,
    estimate_from_log_weights,
    exact_discrete_expectation,
    logsumexp,
    run_importance_sampling,
)

DOMAIN = list(range(10))
F = lambda x: x  # estimand: E_target[X]


def make_target() -> DiscreteDistribution:
    # Peaked, increasing target over 0..9.
    return DiscreteDistribution({x: (x + 1) ** 2 for x in DOMAIN})


def make_uniform() -> DiscreteDistribution:
    return DiscreteDistribution({x: 1.0 for x in DOMAIN})


def replicate_estimates(reps, n, target, proposal, seed):
    """Return (ordinary[], self_normalized[]) over `reps` replications."""
    rng = random.Random(seed)
    ordinary, self_norm = [], []
    for _ in range(reps):
        res = run_importance_sampling(
            n, target.logpdf, proposal.logpdf, proposal.sample, F,
            rng=rng, seed=seed)
        assert res.available
        ordinary.append(res.ordinary)
        self_norm.append(res.self_normalized)
    return ordinary, self_norm


class TestExactIdentities(unittest.TestCase):
    def test_logsumexp(self):
        # [EXACT]
        self.assertAlmostEqual(logsumexp([0.0, 0.0]), math.log(2.0))
        self.assertEqual(logsumexp([float("-inf"), float("-inf")]), float("-inf"))

    def test_ess_equal_weights_equals_n(self):
        # [EXACT] equal weights -> ESS == n.
        n = 100
        res = estimate_from_log_weights([1.0] * n, [0.7] * n, seed=1)
        self.assertTrue(res.available)
        self.assertAlmostEqual(res.ess, n, places=9)

    def test_exact_discrete_expectation(self):
        # [EXACT] closed-form check on a tiny domain.
        dist = DiscreteDistribution({0: 1.0, 1: 3.0})
        self.assertAlmostEqual(exact_discrete_expectation(dist, F), 0.75)

    def test_self_normalized_invariant_to_target_scale(self):
        # [EXACT] scaling the (unnormalized) target leaves T_sn unchanged.
        values = [1.0, 2.0, 3.0]
        lw1 = [0.0, 0.5, -0.25]
        c = 2.3
        lw2 = [lw + c for lw in lw1]
        r1 = estimate_from_log_weights(values, lw1)
        r2 = estimate_from_log_weights(values, lw2)
        self.assertAlmostEqual(r1.self_normalized, r2.self_normalized, places=12)
        self.assertAlmostEqual(r1.ess, r2.ess, places=12)
        # Ordinary estimate rescales by exp(c): it needs a normalized target.
        self.assertAlmostEqual(r2.ordinary, r1.ordinary * math.exp(c), places=9)


class TestBiasProperties(unittest.TestCase):
    """[EMPIRICAL] Ordinary estimator unbiased; self-normalized biased for
    finite n, with bias shrinking as n grows. Fixed seeds, z-test tolerances."""

    def test_ordinary_unbiased_self_normalized_biased(self):
        target, proposal = make_target(), make_uniform()
        exact = exact_discrete_expectation(target, F)
        reps, n = 20000, 5
        ordinary, self_norm = replicate_estimates(reps, n, target, proposal, seed=2024)

        def mean_and_se(xs):
            m = sum(xs) / len(xs)
            var = sum((x - m) ** 2 for x in xs) / (len(xs) - 1)
            return m, math.sqrt(var / len(xs))

        mo, seo = mean_and_se(ordinary)
        ms, ses = mean_and_se(self_norm)
        # Ordinary: consistent with exact (within 4 standard errors).
        self.assertLess(abs(mo - exact), 4 * seo,
                        f"ordinary mean {mo:.4f} vs exact {exact:.4f}")
        # Self-normalized: bias statistically detectable at small n (> 4 SE).
        self.assertGreater(abs(ms - exact), 4 * ses,
                           f"self-normalized bias at n={n} not detectable")

    def test_self_normalized_bias_shrinks_with_n(self):
        target, proposal = make_target(), make_uniform()
        exact = exact_discrete_expectation(target, F)
        _, sn_small = replicate_estimates(20000, 5, target, proposal, seed=7)
        _, sn_large = replicate_estimates(2000, 500, target, proposal, seed=8)
        bias_small = abs(sum(sn_small) / len(sn_small) - exact)
        bias_large = abs(sum(sn_large) / len(sn_large) - exact)
        self.assertLess(bias_large, bias_small)

    def test_self_normalized_recovers_unnormalized_target(self):
        # [EMPIRICAL] target scaled by 3: ordinary is off by ~3x,
        # self-normalized still converges to the exact expectation.
        target, proposal = make_target(), make_uniform()
        exact = exact_discrete_expectation(target, F)
        log3 = math.log(3.0)
        unnorm_logpdf = lambda x: target.logpdf(x) + log3
        rng = random.Random(99)
        res = run_importance_sampling(
            20000, unnorm_logpdf, proposal.logpdf, proposal.sample, F,
            rng=rng, seed=99)
        self.assertTrue(res.available)
        self.assertAlmostEqual(res.self_normalized, exact, delta=0.05)
        self.assertAlmostEqual(res.ordinary, 3.0 * exact, delta=0.2)


class TestDiagnostics(unittest.TestCase):
    def test_zero_coverage_discrete_reports_unavailable(self):
        target = make_target()
        bad_proposal = DiscreteDistribution({x: 1.0 for x in DOMAIN if x < 5})
        diag = check_discrete_coverage(DOMAIN, target.pdf, bad_proposal.pdf)
        self.assertIsNotNone(diag)
        self.assertEqual(diag.code, ZERO_COVERAGE)
        self.assertEqual(diag.severity, "error")
        self.assertIn("unavailable", diag.message)
        # Full-coverage proposal -> no diagnostic.
        self.assertIsNone(check_discrete_coverage(DOMAIN, target.pdf, make_uniform().pdf))

    def test_proposal_zero_target_positive_gives_infinite_weight(self):
        # A draw with q(x)=0, p(x)>0 -> infinite weight -> estimates unavailable.
        res = estimate_from_log_weights([1.0, 2.0], [0.0, float("inf")], seed=3)
        self.assertFalse(res.available)
        self.assertEqual(res.errors()[0].code, INFINITE_WEIGHT)
        self.assertIsNone(res.ess)

    def test_all_weights_zero(self):
        res = estimate_from_log_weights([1.0, 2.0], [float("-inf")] * 2)
        self.assertFalse(res.available)
        self.assertEqual(res.errors()[0].code, ALL_WEIGHTS_ZERO)

    def test_empty_sample(self):
        res = estimate_from_log_weights([], [])
        self.assertFalse(res.available)
        self.assertEqual(res.errors()[0].code, EMPTY_SAMPLE)

    def test_low_ess_warning(self):
        # One dominant weight -> ESS ~ 1 << n.
        logw = [0.0] + [-30.0] * 49
        res = estimate_from_log_weights([1.0] * 50, logw)
        self.assertTrue(res.available)
        self.assertLess(res.ess, 2.0)
        codes = [d.code for d in res.warnings()]
        self.assertIn(LOW_ESS, codes)

    def test_seed_and_n_recorded(self):
        target, proposal = make_target(), make_uniform()
        rng = random.Random(123)
        res = run_importance_sampling(
            250, target.logpdf, proposal.logpdf, proposal.sample, F,
            rng=rng, seed=123)
        self.assertEqual(res.seed, 123)
        self.assertEqual(res.n, 250)


class TestMixtureProposal(unittest.TestCase):
    def test_mixture_logpdf_matches_manual_mixture(self):
        # [EXACT] mixture density equals the weighted sum of components.
        q1 = DiscreteDistribution({x: 1.0 for x in DOMAIN})
        q2 = DiscreteDistribution({x: (10 - x) for x in DOMAIN})
        mix = MixtureProposal([q1, q2], [0.25, 0.75])
        for x in DOMAIN:
            manual = 0.25 * q1.pdf(x) + 0.75 * q2.pdf(x)
            self.assertAlmostEqual(mix.pdf(x), manual, places=12)

    def test_mixture_estimates_converge(self):
        # [EMPIRICAL] IS with a mixture proposal recovers the exact mean.
        target = make_target()
        q1 = DiscreteDistribution({x: 1.0 for x in DOMAIN})
        q2 = DiscreteDistribution({x: (x + 1) for x in DOMAIN})
        mix = MixtureProposal([q1, q2], [0.5, 0.5])
        exact = exact_discrete_expectation(target, F)
        rng = random.Random(555)
        res = run_importance_sampling(
            20000, target.logpdf, mix.logpdf, mix.sample, F, rng=rng, seed=555)
        self.assertTrue(res.available)
        self.assertAlmostEqual(res.ordinary, exact, delta=0.05)
        self.assertAlmostEqual(res.self_normalized, exact, delta=0.05)


class TestAdaptiveIS(unittest.TestCase):
    def test_checkpoint_restore_preserves_weights(self):
        # Weights frozen at generation time must survive checkpoint/restore
        # and must NOT be recomputed with the newer proposal density.
        target = make_target()
        q1 = make_uniform()
        q2 = DiscreteDistribution({x: (x + 1) for x in DOMAIN})

        adaptive = AdaptiveIS(target.logpdf, F, rng=random.Random(11), seed=11)
        rec1 = adaptive.run_round(40, q1.logpdf, q1.sample, description="q1-uniform")

        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "ckpt.json"
            adaptive.save_checkpoint(path)
            restored = AdaptiveIS.load_checkpoint(
                path, target.logpdf, F, rng=random.Random(22))

        # Restored weights identical to the frozen ones.
        self.assertEqual(restored.rounds[0].log_weights, rec1.log_weights)
        self.assertEqual(restored.seed, 11)
        # Stored weights match q1 (the true generating density) ...
        for x, lw in zip(rec1.samples, restored.rounds[0].log_weights):
            self.assertAlmostEqual(lw, target.logpdf(x) - q1.logpdf(x), places=12)
        # ... and differ from what a recomputation with q2 would give.
        self.assertTrue(any(
            abs(lw - (target.logpdf(x) - q2.logpdf(x))) > 1e-9
            for x, lw in zip(rec1.samples, restored.rounds[0].log_weights)))

        # Continue adaptively with q2 after restore; pooled estimates work.
        restored.run_round(200, q2.logpdf, q2.sample, description="q2-adapted")
        res = restored.estimates()
        self.assertTrue(res.available)
        self.assertEqual(res.n, 240)
        self.assertEqual(res.seed, 11)
        exact = exact_discrete_expectation(target, F)
        self.assertAlmostEqual(res.self_normalized, exact, delta=0.3)

    def test_checkpoint_roundtrip_nonfinite_weights(self):
        target = make_target()
        q1 = make_uniform()
        adaptive = AdaptiveIS(target.logpdf, F, rng=random.Random(5), seed=5)
        adaptive.run_round(10, q1.logpdf, q1.sample)
        # Inject non-finite weights to exercise the encoding.
        adaptive.rounds[0].log_weights[0] = float("inf")
        adaptive.rounds[0].log_weights[1] = float("-inf")
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "ckpt.json"
            adaptive.save_checkpoint(path)
            restored = AdaptiveIS.load_checkpoint(path, target.logpdf, F)
        self.assertEqual(restored.rounds[0].log_weights[0], float("inf"))
        self.assertEqual(restored.rounds[0].log_weights[1], float("-inf"))
        # The infinite weight must surface as an error on the pooled estimate.
        res = restored.estimates()
        self.assertFalse(res.available)
        self.assertEqual(res.errors()[0].code, INFINITE_WEIGHT)


if __name__ == "__main__":
    unittest.main()
