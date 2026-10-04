import random
import unittest

from hmm import (
    HMM,
    MISSING,
    estimate_state_posterior,
    forward_backward,
    sample_sequence,
    sample_sequences,
)


def make_model():
    return HMM(
        n_states=2,
        initial=(0.5, 0.5),
        transition=((0.7, 0.3), (0.3, 0.7)),
        emission=({"a": 0.8, "b": 0.2}, {"a": 0.2, "b": 0.8}),
    )


class TestSampling(unittest.TestCase):
    def test_caller_injected_rng_is_reproducible(self):
        m = make_model()
        s1 = sample_sequences(m, 5, 20, random.Random(42), seed=42)
        s2 = sample_sequences(m, 5, 20, random.Random(42), seed=42)
        self.assertEqual(s1.states, s2.states)
        self.assertEqual(s1.observations, s2.observations)

    def test_metadata_records_seed_and_sample_size(self):
        m = make_model()
        batch = sample_sequences(m, 4, 100, random.Random(7), seed=7)
        self.assertEqual(batch.kind, "empirical")
        self.assertEqual(batch.seed, 7)
        self.assertEqual(batch.n_samples, 100)
        self.assertEqual(len(batch.states), 100)
        self.assertTrue(all(len(s) == 4 for s in batch.states))

    def test_sampled_symbols_come_from_support(self):
        m = make_model()
        rng = random.Random(1)
        states, obs = sample_sequence(m, 50, rng)
        self.assertTrue(all(s in (0, 1) for s in states))
        self.assertTrue(all(o in ("a", "b") for o in obs))

    def test_empirical_posterior_close_to_exact(self):
        m = make_model()
        obs = ("a", "a")
        exact = forward_backward(m, obs)
        self.assertEqual(exact.kind, "exact")
        est = estimate_state_posterior(m, obs, time=1, n=20000,
                                       rng=random.Random(2024), seed=2024)
        self.assertEqual(est.kind, "empirical")
        self.assertEqual(est.seed, 2024)
        self.assertEqual(est.n_samples, 20000)
        self.assertGreater(est.n_accepted, 0)
        for e, x in zip(est.estimates, exact.posteriors[1]):
            self.assertAlmostEqual(e, x, delta=0.05)

    def test_empirical_with_missing_observation(self):
        m = make_model()
        obs = ("a", MISSING)
        exact = forward_backward(m, obs)
        est = estimate_state_posterior(m, obs, time=1, n=20000,
                                       rng=random.Random(99), seed=99)
        for e, x in zip(est.estimates, exact.posteriors[1]):
            self.assertAlmostEqual(e, x, delta=0.05)

    def test_no_matching_samples_gives_none_not_fake_estimate(self):
        m = make_model()
        # 'q' is emitted by nobody: rejection sampling accepts nothing.
        est = estimate_state_posterior(m, ("q",), time=0, n=100,
                                       rng=random.Random(3), seed=3)
        self.assertEqual(est.n_accepted, 0)
        self.assertIsNone(est.estimates)


if __name__ == "__main__":
    unittest.main()
