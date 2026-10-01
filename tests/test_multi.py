import unittest

from bounded_mean import (
    MET,
    NOT_MET,
    AdaptiveMultiEstimator,
    RecordingRandom,
    VariableSpec,
)


def bernoulli_sampler(p):
    def sample(rng):
        return 1.0 if rng.random() < p else 0.0
    return sample


class TestAllocation(unittest.TestCase):
    def test_allocation_must_not_exceed_delta(self):
        variables = [
            VariableSpec("a", 0.0, 1.0, 0.1, bernoulli_sampler(0.5)),
            VariableSpec("b", 0.0, 1.0, 0.1, bernoulli_sampler(0.5)),
        ]
        with self.assertRaises(ValueError):
            AdaptiveMultiEstimator(
                variables=variables, delta=0.1, budget=100,
                rng=RecordingRandom(seed=1),
                allocation={"a": 0.08, "b": 0.08},
            )

    def test_default_equal_allocation(self):
        variables = [
            VariableSpec("a", 0.0, 1.0, 0.1, bernoulli_sampler(0.5)),
            VariableSpec("b", 0.0, 1.0, 0.1, bernoulli_sampler(0.5)),
        ]
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.1, budget=10, rng=RecordingRandom(seed=1)
        )
        self.assertAlmostEqual(sum(est._shares.values()), 0.1)


class TestAdaptiveSelection(unittest.TestCase):
    def test_all_met_and_constant_fast(self):
        variables = [
            VariableSpec("easy_const", 0.7, 0.7, 0.01, lambda r: 0.7),
            VariableSpec("loose", 0.0, 1.0, 0.2, bernoulli_sampler(0.5)),
            VariableSpec("tight", 0.0, 1.0, 0.03, bernoulli_sampler(0.3)),
        ]
        rng = RecordingRandom(seed=123)
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.1, budget=100000, rng=rng
        )
        res = est.run()
        self.assertEqual(res.status, MET)
        by_name = {v.name: v for v in res.variables}
        # Constant variable: exact, zero samples.
        self.assertEqual(by_name["easy_const"].n_samples, 0)
        self.assertEqual(by_name["easy_const"].conclusion_kind, "exact")
        # Adaptive policy concentrates samples on the hardest variable.
        self.assertGreater(by_name["tight"].n_samples,
                           by_name["loose"].n_samples)
        self.assertLess(res.draws_used, 100000)

    def test_budget_exhausted_lists_unmet(self):
        # "reachable" needs only its first sample (radius at n=1 is ~1.45);
        # "hopeless" then absorbs every remaining draw of the budget.
        variables = [
            VariableSpec("reachable", 0.0, 1.0, 1.5, bernoulli_sampler(0.5)),
            VariableSpec("hopeless", 0.0, 1.0, 1e-4, bernoulli_sampler(0.5)),
        ]
        rng = RecordingRandom(seed=9)
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.1, budget=300, rng=rng
        )
        res = est.run()
        self.assertEqual(res.status, NOT_MET)
        by_name = {v.name: v for v in res.variables}
        self.assertEqual(by_name["reachable"].status, MET)
        self.assertEqual(by_name["hopeless"].status, NOT_MET)
        self.assertEqual(res.draws_used, 300)
        self.assertIn("NOT", res.guarantee)


class TestSharedSamples(unittest.TestCase):
    def test_joint_sampler_correlated_variables(self):
        # Perfectly correlated pair from one shared uniform draw; the
        # union-bound guarantee does not rely on independence.
        def joint(rng):
            u = rng.random()
            return {"x": u, "y": u}

        variables = [
            VariableSpec("x", 0.0, 1.0, 0.05),
            VariableSpec("y", 0.0, 1.0, 0.05),
        ]
        rng = RecordingRandom(seed=2024)
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.1, budget=50000,
            rng=rng, joint_sampler=joint,
        )
        res = est.run()
        self.assertEqual(res.status, MET)
        self.assertTrue(res.shared_samples)
        by_name = {v.name: v for v in res.variables}
        # Shared draws: both variables see the same number of samples.
        self.assertEqual(by_name["x"].n_samples, by_name["y"].n_samples)
        # Estimates of the identical shared sequence must coincide.
        self.assertAlmostEqual(by_name["x"].estimate, by_name["y"].estimate)

    def test_joint_unknown_variable_rejected(self):
        variables = [VariableSpec("x", 0.0, 1.0, 0.1)]
        est = AdaptiveMultiEstimator(
            variables=variables, delta=0.1, budget=10,
            rng=RecordingRandom(seed=1),
            joint_sampler=lambda r: {"z": 0.5},
        )
        with self.assertRaises(ValueError):
            est.run()


if __name__ == "__main__":
    unittest.main()
