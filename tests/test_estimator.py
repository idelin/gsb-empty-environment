import math
import random
import unittest

from bounded_estimation import (
    ConclusionKind,
    Status,
    estimate_mean,
)


def bernoulli_sampler(p):
    def sampler(rng):
        return 1.0 if rng.random() < p else 0.0

    return sampler


class TestConvergence(unittest.TestCase):
    def test_bernoulli_converges_within_epsilon(self):
        result = estimate_mean(
            sampler=bernoulli_sampler(0.3),
            low=0.0,
            high=1.0,
            epsilon=0.05,
            delta=0.05,
            max_samples=100_000,
            seed=20261001,
        )
        self.assertEqual(result.status, Status.CONVERGED)
        self.assertTrue(result.converged)
        self.assertTrue(result.target_met)
        self.assertLessEqual(result.radius, 0.05)
        self.assertAlmostEqual(result.estimate, 0.3, delta=0.05)
        self.assertLessEqual(result.n, 100_000)

    def test_labels_and_seed_recorded(self):
        result = estimate_mean(
            sampler=bernoulli_sampler(0.5),
            low=0.0,
            high=1.0,
            epsilon=0.1,
            delta=0.1,
            max_samples=10_000,
            seed=42,
        )
        self.assertEqual(result.estimate_kind, ConclusionKind.EMPIRICAL)
        self.assertEqual(result.guarantee_kind, ConclusionKind.EXACT)
        self.assertEqual(result.seed, 42)
        self.assertGreater(result.n, 0)
        self.assertTrue(result.evidence)
        self.assertEqual(result.evidence[-1].n, result.n)

    def test_reproducible_with_same_seed(self):
        kwargs = dict(
            sampler=bernoulli_sampler(0.7),
            low=0.0,
            high=1.0,
            epsilon=0.05,
            delta=0.05,
            max_samples=50_000,
            seed=7,
        )
        first = estimate_mean(**kwargs)
        second = estimate_mean(**kwargs)
        self.assertEqual(first.estimate, second.estimate)
        self.assertEqual(first.n, second.n)

    def test_injected_rng_is_used(self):
        rng = random.Random(123)
        result = estimate_mean(
            sampler=bernoulli_sampler(0.5),
            low=0.0,
            high=1.0,
            epsilon=0.1,
            delta=0.1,
            max_samples=10_000,
            rng=rng,
            seed=123,
        )
        expected = estimate_mean(
            sampler=bernoulli_sampler(0.5),
            low=0.0,
            high=1.0,
            epsilon=0.1,
            delta=0.1,
            max_samples=10_000,
            seed=123,
        )
        self.assertEqual(result.estimate, expected.estimate)


class TestConstantVariable(unittest.TestCase):
    def test_constant_converges_quickly(self):
        result = estimate_mean(
            sampler=lambda rng: 0.7,
            low=0.0,
            high=1.0,
            epsilon=0.01,
            delta=0.05,
            max_samples=1_000_000,
            seed=1,
        )
        self.assertTrue(result.converged)
        self.assertEqual(result.estimate, 0.7)
        # Hoeffding alone would need ~26k samples; the constant-observation
        # bound converges in well under 2k.
        self.assertLessEqual(result.n, 2048)
        self.assertTrue(all(rec.all_equal for rec in result.evidence))


class TestBudgetExhausted(unittest.TestCase):
    def test_never_claims_convergence_when_budget_runs_out(self):
        result = estimate_mean(
            sampler=bernoulli_sampler(0.5),
            low=0.0,
            high=1.0,
            epsilon=1e-6,
            delta=0.05,
            max_samples=64,
            seed=9,
        )
        self.assertEqual(result.status, Status.BUDGET_EXHAUSTED)
        self.assertFalse(result.converged)
        self.assertFalse(result.target_met)
        self.assertGreater(result.radius, result.epsilon)
        self.assertEqual(result.n, 64)

    def test_all_stopping_evidence_saved(self):
        result = estimate_mean(
            sampler=bernoulli_sampler(0.5),
            low=0.0,
            high=1.0,
            epsilon=1e-6,
            delta=0.05,
            max_samples=100,
            seed=9,
            initial_stage=8,
        )
        # stages at 8, 16, 32, 64, then a forced check at the cap of 100
        self.assertEqual([rec.n for rec in result.evidence], [8, 16, 32, 64, 100])
        self.assertTrue(result.evidence[-1].forced)
        self.assertFalse(any(rec.target_met for rec in result.evidence))
        for rec in result.evidence:
            self.assertEqual(rec.kind, ConclusionKind.EMPIRICAL)
            self.assertGreater(rec.stage_delta, 0.0)


class TestRepeatedPeekingCoverage(unittest.TestCase):
    def test_empirical_failure_rate_within_delta(self):
        # EMPIRICAL check (simulation): the staged procedure peeks repeatedly,
        # yet the overall failure rate must stay below delta.  A single
        # fixed-sample bound reused at every peek would not give this.
        delta, trials = 0.2, 300
        failures = 0
        for t in range(trials):
            result = estimate_mean(
                sampler=bernoulli_sampler(0.5),
                low=0.0,
                high=1.0,
                epsilon=0.1,
                delta=delta,
                max_samples=50_000,
                seed=10_000 + t,
            )
            self.assertTrue(result.converged)
            if abs(result.estimate - 0.5) > result.radius:
                failures += 1
        margin = 4.0 * math.sqrt(delta * (1.0 - delta) / trials)
        self.assertLessEqual(failures / trials, delta + margin)


class TestSamplerContract(unittest.TestCase):
    def test_out_of_bounds_sample_rejected(self):
        with self.assertRaises(ValueError):
            estimate_mean(
                sampler=lambda rng: 1.5,
                low=0.0,
                high=1.0,
                epsilon=0.1,
                delta=0.1,
                max_samples=100,
                seed=0,
            )


if __name__ == "__main__":
    unittest.main()
