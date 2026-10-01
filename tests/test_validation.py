import unittest

from bounded_estimation import (
    AdaptiveMultiEstimator,
    StagedTracker,
    VariableSpec,
    estimate_mean,
)
from bounded_estimation.confidence import hoeffding_radius


GOOD = dict(
    sampler=lambda rng: 0.5,
    low=0.0,
    high=1.0,
    epsilon=0.1,
    delta=0.1,
    max_samples=1000,
    seed=0,
)


class TestEstimateMeanValidation(unittest.TestCase):
    def test_delta_out_of_range(self):
        for bad in (0.0, 1.0, -0.1, 1.5, float("nan"), float("inf")):
            with self.subTest(delta=bad):
                with self.assertRaises(ValueError):
                    estimate_mean(**{**GOOD, "delta": bad})

    def test_epsilon_out_of_range(self):
        for bad in (0.0, -1.0, float("nan"), float("inf")):
            with self.subTest(epsilon=bad):
                with self.assertRaises(ValueError):
                    estimate_mean(**{**GOOD, "epsilon": bad})

    def test_bounds_inverted_or_equal(self):
        for low, high in [(1.0, 1.0), (2.0, 1.0), (float("-inf"), 1.0)]:
            with self.subTest(low=low, high=high):
                with self.assertRaises(ValueError):
                    estimate_mean(**{**GOOD, "low": low, "high": high})

    def test_budget_and_schedule(self):
        with self.assertRaises(ValueError):
            estimate_mean(**{**GOOD, "max_samples": 0})
        with self.assertRaises(ValueError):
            estimate_mean(**{**GOOD, "initial_stage": 0})
        with self.assertRaises(ValueError):
            estimate_mean(**{**GOOD, "growth": 1.0})


class TestTrackerValidation(unittest.TestCase):
    def test_check_only_at_declared_sizes(self):
        tracker = StagedTracker(low=0.0, high=1.0, epsilon=0.1, delta=0.1,
                                initial_stage=4)
        tracker.add(0.5)
        with self.assertRaises(RuntimeError):
            tracker.check()  # n=1 is not a pre-declared stage size
        for _ in range(3):
            tracker.add(0.5)
        tracker.check()  # n=4 is the first declared size
        self.assertEqual(len(tracker.records), 1)


class TestMultiValidation(unittest.TestCase):
    def make(self, **overrides):
        kwargs = dict(
            variables=[
                VariableSpec(name="x", low=0.0, high=1.0, epsilon=0.1,
                             sampler=lambda rng: 0.5),
            ],
            delta=0.1,
            max_samples=1000,
            seed=0,
        )
        kwargs.update(overrides)
        return AdaptiveMultiEstimator(**kwargs)

    def test_empty_variables(self):
        with self.assertRaises(ValueError):
            self.make(variables=[])

    def test_duplicate_names(self):
        dup = [
            VariableSpec(name="x", low=0.0, high=1.0, epsilon=0.1,
                         sampler=lambda rng: 0.5),
            VariableSpec(name="x", low=0.0, high=1.0, epsilon=0.1,
                         sampler=lambda rng: 0.5),
        ]
        with self.assertRaises(ValueError):
            self.make(variables=dup)

    def test_bad_weight(self):
        bad = [
            VariableSpec(name="x", low=0.0, high=1.0, epsilon=0.1,
                         sampler=lambda rng: 0.5, weight=0.0),
        ]
        with self.assertRaises(ValueError):
            self.make(variables=bad)

    def test_missing_sampler(self):
        with self.assertRaises(ValueError):
            self.make(variables=[VariableSpec(name="x", low=0.0, high=1.0,
                                              epsilon=0.1)])

    def test_bad_delta(self):
        with self.assertRaises(ValueError):
            self.make(delta=1.0)


class TestConfidenceFunctionValidation(unittest.TestCase):
    def test_hoeffding_rejects_bad_inputs(self):
        with self.assertRaises(ValueError):
            hoeffding_radius(0, 0.0, 1.0, 0.1)
        with self.assertRaises(ValueError):
            hoeffding_radius(10, 0.0, 1.0, 1.0)
        with self.assertRaises(ValueError):
            hoeffding_radius(10, 1.0, 0.0, 0.1)


if __name__ == "__main__":
    unittest.main()
