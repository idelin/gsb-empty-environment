import math
import unittest

from bounded_estimation import (
    AdaptiveMultiEstimator,
    ConclusionKind,
    Status,
    VariableSpec,
)


def bernoulli_sampler(p):
    def sampler(rng):
        return 1.0 if rng.random() < p else 0.0

    return sampler


def make_three_variables():
    return [
        VariableSpec(
            name="rare", low=0.0, high=1.0, epsilon=0.05,
            sampler=bernoulli_sampler(0.2), weight=1.0,
        ),
        VariableSpec(
            name="noisy", low=0.0, high=1.0, epsilon=0.05,
            sampler=lambda rng: rng.random(), weight=1.0,
        ),
        VariableSpec(
            name="constant", low=0.0, high=1.0, epsilon=0.01,
            sampler=lambda rng: 0.25, weight=2.0,
        ),
    ]


class TestAdaptiveMulti(unittest.TestCase):
    def test_all_variables_converge(self):
        est = AdaptiveMultiEstimator(
            variables=make_three_variables(),
            delta=0.05,
            max_samples=500_000,
            seed=20261001,
        )
        result = est.run()
        self.assertEqual(result.status, Status.CONVERGED)
        self.assertTrue(result.converged)
        self.assertEqual(result.seed, 20261001)
        for name, res in result.results.items():
            with self.subTest(variable=name):
                self.assertTrue(res.converged)
                self.assertLessEqual(res.radius, res.epsilon)
                self.assertEqual(res.estimate_kind, ConclusionKind.EMPIRICAL)
                self.assertEqual(res.guarantee_kind, ConclusionKind.EXACT)
                self.assertGreater(res.n, 0)
                self.assertTrue(res.evidence)
        self.assertAlmostEqual(result.results["constant"].estimate, 0.25)
        self.assertAlmostEqual(result.results["rare"].estimate, 0.2, delta=0.05)
        self.assertAlmostEqual(result.results["noisy"].estimate, 0.5, delta=0.05)

    def test_delta_allocation_is_predeclared_and_sums_to_delta(self):
        variables = make_three_variables()
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.04, max_samples=1000, seed=1
        )
        total = sum(est._delta_alloc.values())
        self.assertAlmostEqual(total, 0.04)
        # weights 1, 1, 2 -> allocations proportional to weights
        self.assertAlmostEqual(
            est._delta_alloc["constant"], 2.0 * est._delta_alloc["rare"]
        )

    def test_adaptive_selection_targets_widest_variable(self):
        est = AdaptiveMultiEstimator(
            variables=make_three_variables(),
            delta=0.05,
            max_samples=500_000,
            seed=3,
        )
        result = est.run()
        self.assertTrue(result.selection_log)
        # Once the constant variable has converged, it is never selected again.
        converged_step = None
        for record in result.selection_log:
            if record.variable == "constant":
                converged_step = record.step
        later = [
            r for r in result.selection_log
            if converged_step is not None and r.step > converged_step
        ]
        self.assertTrue(all(r.variable != "constant" for r in later))
        # The constant variable converges with far fewer samples than the others.
        self.assertLess(
            result.results["constant"].n, result.results["rare"].n
        )

    def test_budget_exhausted_marks_only_unmet_variables(self):
        variables = [
            VariableSpec(
                name="easy", low=0.0, high=1.0, epsilon=0.2,
                sampler=bernoulli_sampler(0.5),
            ),
            VariableSpec(
                name="impossible", low=0.0, high=1.0, epsilon=1e-9,
                sampler=bernoulli_sampler(0.5),
            ),
        ]
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.1, max_samples=300, seed=5
        )
        result = est.run()
        self.assertEqual(result.status, Status.BUDGET_EXHAUSTED)
        self.assertFalse(result.converged)
        self.assertTrue(result.results["easy"].converged)
        self.assertFalse(result.results["impossible"].converged)
        self.assertFalse(result.results["impossible"].target_met)
        self.assertLessEqual(result.total_draws, 300)
        # The hopeless-variable determination is preserved as evidence.
        hopeless = [r for r in result.selection_log
                    if r.variable == "impossible" and "hopeless" in r.reason]
        self.assertTrue(hopeless)


class TestCorrelatedSharedSamples(unittest.TestCase):
    def test_joint_sampler_shares_draws(self):
        # Perfectly (anti-)correlated variables fed by ONE shared uniform draw.
        def joint(rng):
            u = rng.random()
            return {"up": u, "down": 1.0 - u}

        variables = [
            VariableSpec(name="up", low=0.0, high=1.0, epsilon=0.05),
            VariableSpec(name="down", low=0.0, high=1.0, epsilon=0.05),
        ]
        est = AdaptiveMultiEstimator(
            variables=variables,
            delta=0.1,
            max_samples=200_000,
            seed=11,
            joint_sampler=joint,
        )
        result = est.run()
        self.assertTrue(result.converged)
        # Shared draws: both variables see exactly one value per draw.
        self.assertEqual(
            result.results["up"].n, result.results["down"].n
        )
        self.assertAlmostEqual(
            result.results["up"].estimate + result.results["down"].estimate, 1.0
        )

    def test_coverage_holds_despite_correlation(self):
        # EMPIRICAL check: with perfectly correlated shared samples, treating
        # them as independent would understate the joint failure probability.
        # The union bound used here does not assume independence, so the
        # overall failure rate must still stay below delta.
        delta, trials = 0.2, 200
        failures = 0
        for t in range(trials):
            def joint(rng):
                u = rng.random()
                return {"a": u, "b": u}  # identical -> maximally correlated

            variables = [
                VariableSpec(name="a", low=0.0, high=1.0, epsilon=0.1),
                VariableSpec(name="b", low=0.0, high=1.0, epsilon=0.1),
            ]
            est = AdaptiveMultiEstimator(
                variables=variables,
                delta=delta,
                max_samples=100_000,
                seed=20_000 + t,
                joint_sampler=joint,
            )
            result = est.run()
            self.assertTrue(result.converged)
            for name, truth in (("a", 0.5), ("b", 0.5)):
                res = result.results[name]
                if abs(res.estimate - truth) > res.radius:
                    failures += 1
                    break
        margin = 4.0 * math.sqrt(delta * (1.0 - delta) / trials)
        self.assertLessEqual(failures / trials, delta + margin)


if __name__ == "__main__":
    unittest.main()
