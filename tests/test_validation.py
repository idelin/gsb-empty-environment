import unittest

from bounded_mean import (
    AdaptiveMultiEstimator,
    RecordingRandom,
    StagedEstimator,
    VariableSpec,
)


class TestParameterValidation(unittest.TestCase):
    def test_single_estimator_rejects_bad_params(self):
        base = dict(name="v", low=0.0, high=1.0, epsilon=0.1, delta=0.05,
                    budget=10, sampler=lambda r: 0.5, rng=RecordingRandom(seed=1))
        for key, bad in (("delta", 0.0), ("delta", 1.0), ("delta", 2.0),
                         ("epsilon", 0.0), ("epsilon", -0.5),
                         ("budget", 0), ("budget", -3)):
            with self.assertRaises(ValueError, msg=f"{key}={bad}"):
                StagedEstimator(**{**base, key: bad})
        with self.assertRaises(ValueError):
            StagedEstimator(**{**base, "low": 2.0, "high": 1.0})

    def test_checkpoints_must_be_predeclared_ordered(self):
        base = dict(name="v", low=0.0, high=1.0, epsilon=0.1, delta=0.05,
                    budget=10, sampler=lambda r: 0.5, rng=RecordingRandom(seed=1))
        with self.assertRaises(ValueError):
            StagedEstimator(**base, checkpoints=[5, 5, 9])
        with self.assertRaises(ValueError):
            StagedEstimator(**base, checkpoints=[5, 3])
        with self.assertRaises(ValueError):
            StagedEstimator(**base, checkpoints=[5, 20])  # beyond budget
        with self.assertRaises(ValueError):
            StagedEstimator(**base, checkpoints=[])

    def test_multi_rejects_bad_params(self):
        spec = VariableSpec("a", 0.0, 1.0, 0.1, lambda r: 0.5)
        with self.assertRaises(ValueError):
            AdaptiveMultiEstimator(variables=[spec], delta=1.5, budget=10,
                                   rng=RecordingRandom(seed=1))
        with self.assertRaises(ValueError):
            AdaptiveMultiEstimator(variables=[spec], delta=0.1, budget=0,
                                   rng=RecordingRandom(seed=1))
        with self.assertRaises(ValueError):
            AdaptiveMultiEstimator(variables=[], delta=0.1, budget=10,
                                   rng=RecordingRandom(seed=1))
        dup = [spec, VariableSpec("a", 0.0, 1.0, 0.1, lambda r: 0.5)]
        with self.assertRaises(ValueError):
            AdaptiveMultiEstimator(variables=dup, delta=0.1, budget=10,
                                   rng=RecordingRandom(seed=1))


if __name__ == "__main__":
    unittest.main()
