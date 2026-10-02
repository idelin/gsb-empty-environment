import random
import unittest

from hmm import HMM, estimate_state_frequencies, forward_backward, sample_path


def model():
    return HMM(
        initial=[0.6, 0.4],
        transition=[[0.7, 0.3], [0.4, 0.6]],
        emission=[{"x": 0.9, "y": 0.1}, {"x": 0.2, "y": 0.8}],
    )


class TestSampling(unittest.TestCase):
    def test_caller_injected_rng_makes_sampling_reproducible(self):
        m = model()
        states1, obs1 = sample_path(m, 50, random.Random(1234))
        states2, obs2 = sample_path(m, 50, random.Random(1234))
        self.assertEqual(states1, states2)
        self.assertEqual(obs1, obs2)
        states3, _ = sample_path(m, 50, random.Random(4321))
        self.assertNotEqual(states1, states3)

    def test_sampled_lengths_and_empty_path(self):
        m = model()
        states, obs = sample_path(m, 0, random.Random(1))
        self.assertEqual(states, [])
        self.assertEqual(obs, [])
        states, obs = sample_path(m, 17, random.Random(1))
        self.assertEqual(len(states), 17)
        self.assertEqual(len(obs), 17)

    def test_empirical_estimate_records_seed_and_size(self):
        m = model()
        est = estimate_state_frequencies(m, length=10, n_samples=500, seed=2024)
        self.assertEqual(est.kind, "empirical")
        self.assertEqual(est.seed, 2024)
        self.assertEqual(est.n_samples, 500)
        self.assertEqual(len(est.state_frequencies), 10)
        for row in est.state_frequencies:
            self.assertAlmostEqual(sum(row), 1.0, places=9)

    def test_empirical_frequencies_approach_exact_marginals(self):
        # Exact marginals of the *unconditional* state distribution evolve as
        # pi A^t; compare against the empirical frequencies (many samples).
        m = model()
        est = estimate_state_frequencies(m, length=5, n_samples=20000, seed=7)
        probs = list(m.initial)
        for t in range(5):
            for s in range(m.n_states):
                self.assertAlmostEqual(est.state_frequencies[t][s], probs[s], delta=0.02)
            A = m.transition_matrix(t)
            probs = [
                sum(probs[i] * A[i][j] for i in range(m.n_states))
                for j in range(m.n_states)
            ]

    def test_exact_and_empirical_results_are_labelled(self):
        m = model()
        exact = forward_backward(m, ["x", "y", "x"])
        self.assertTrue(exact.feasible)  # exact result from dynamic programming
        est = estimate_state_frequencies(m, 3, 100, seed=1)
        self.assertEqual(est.kind, "empirical")


if __name__ == "__main__":
    unittest.main()
