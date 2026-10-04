import math
import unittest

from hmm import (
    HMM,
    MISSING,
    NEG_INF,
    brute_force,
    forward,
    forward_backward,
    forward_backward_scaled,
)


def make_model():
    return HMM(
        n_states=2,
        initial=(0.6, 0.4),
        transition=((0.7, 0.3), (0.4, 0.6)),
        emission=({"a": 0.5, "b": 0.5}, {"a": 0.1, "b": 0.9}),
    )


def make_three_state_model():
    return HMM(
        n_states=3,
        initial=(0.5, 0.3, 0.2),
        transition=(
            (0.6, 0.3, 0.1),
            (0.2, 0.5, 0.3),
            (0.3, 0.3, 0.4),
        ),
        emission=(
            {"x": 0.7, "y": 0.2, "z": 0.1},
            {"x": 0.2, "y": 0.6, "z": 0.2},
            {"x": 0.1, "y": 0.3, "z": 0.6},
        ),
    )


class TestForwardBackward(unittest.TestCase):
    def test_posteriors_sum_to_one(self):
        m = make_model()
        res = forward_backward(m, ("a", "b", "b", "a"))
        self.assertTrue(res.feasible)
        for row in res.posteriors:
            self.assertAlmostEqual(sum(row), 1.0, places=9)

    def test_matches_brute_force(self):
        m = make_three_state_model()
        obs = ("x", "y", "z", "x", "z")
        res = forward_backward(m, obs)
        bf = brute_force(m, obs)
        self.assertAlmostEqual(res.log_evidence, bf.log_evidence, places=9)
        for got, want in zip(res.posteriors, bf.posteriors):
            for g, w in zip(got, want):
                self.assertAlmostEqual(g, w, places=9)
        self.assertEqual(res.viterbi_path, bf.best_path)
        self.assertAlmostEqual(res.viterbi_logprob, bf.best_logprob, places=9)

    def test_matches_brute_force_with_missing(self):
        m = make_three_state_model()
        obs = ("x", MISSING, "z", MISSING, "y")
        res = forward_backward(m, obs)
        bf = brute_force(m, obs)
        self.assertAlmostEqual(res.log_evidence, bf.log_evidence, places=9)
        for got, want in zip(res.posteriors, bf.posteriors):
            for g, w in zip(got, want):
                self.assertAlmostEqual(g, w, places=9)

    def test_all_missing_evidence_is_one(self):
        m = make_model()
        res = forward_backward(m, (MISSING, MISSING, MISSING))
        self.assertAlmostEqual(res.log_evidence, 0.0, places=12)

    def test_log_space_matches_scaled(self):
        m = make_three_state_model()
        obs = ("x", "y", "z", "x", "x", "z", "y") * 30  # T = 210
        res = forward_backward(m, obs)
        log_ev_scaled, gamma_scaled = forward_backward_scaled(m, obs)
        self.assertAlmostEqual(res.log_evidence, log_ev_scaled, places=7)
        for got, want in zip(res.posteriors, gamma_scaled):
            for g, w in zip(got, want):
                self.assertAlmostEqual(g, w, places=7)

    def test_long_sequence_no_underflow(self):
        m = make_model()
        T = 50000
        obs = tuple("ab"[i % 2] for i in range(T))
        res = forward_backward(m, obs)
        self.assertTrue(res.feasible)
        self.assertTrue(math.isfinite(res.log_evidence))
        for row in res.posteriors[:5] + res.posteriors[-5:]:
            self.assertAlmostEqual(sum(row), 1.0, places=6)
        # A naive unscaled forward pass underflows to exactly 0.0.
        alpha = [m.initial[j] * m.emission_prob(j, obs[0]) for j in range(2)]
        for t in range(1, T):
            alpha = [
                m.emission_prob(j, obs[t])
                * sum(alpha[i] * m.transition[i][j] for i in range(2))
                for j in range(2)
            ]
        self.assertEqual(alpha, [0.0, 0.0])  # underflow demonstrated
        self.assertNotEqual(sum(res.posteriors[-1]), 0.0)  # log-space survives


class TestInfeasibility(unittest.TestCase):
    def test_zero_probability_sequence_is_explicitly_infeasible(self):
        m = make_model()
        res = forward_backward(m, ("a", "q", "b"))  # 'q' emitted by nobody
        self.assertFalse(res.feasible)
        self.assertEqual(res.log_evidence, NEG_INF)
        # No fake posterior: posteriors are None, never renormalized zeros.
        self.assertIsNone(res.posteriors)
        self.assertIsNone(res.viterbi_path)
        self.assertEqual(res.viterbi_logprob, NEG_INF)

    def test_impossible_observation_means_no_path(self):
        m = HMM(2, (0.5, 0.5), ((0.5, 0.5), (0.5, 0.5)),
                ({"a": 1.0}, {"b": 1.0}))
        res = forward_backward(m, ("a", "a"))  # state 1 can never emit 'a'...
        self.assertTrue(res.feasible)  # ...but state 0 can: feasible
        res2 = forward_backward(m, ("a", "c"))  # 'c' emitted by nobody
        self.assertFalse(res2.feasible)
        self.assertIsNone(res2.viterbi_path)

    def test_unreachable_state_zero_initial(self):
        m = HMM(2, (1.0, 0.0), ((0.5, 0.5), (0.5, 0.5)),
                ({"a": 1.0}, {"a": 1.0}))
        res = forward_backward(m, ("a", "a"))
        self.assertTrue(res.feasible)
        self.assertEqual(res.posteriors[0], (1.0, 0.0))


class TestEmptySequence(unittest.TestCase):
    def test_empty_sequence_convention(self):
        m = make_model()
        res = forward_backward(m, ())
        self.assertTrue(res.feasible)
        self.assertEqual(res.log_evidence, 0.0)  # evidence = 1
        self.assertEqual(res.posteriors, ())
        self.assertEqual(res.viterbi_path, ())
        self.assertEqual(res.viterbi_logprob, 0.0)


class TestConstraintsAndPiecewise(unittest.TestCase):
    def test_constrained_state_forces_posterior(self):
        m = make_three_state_model()
        obs = ("x", "y", "z")
        cons = (None, {2}, None)  # state at t=1 forced to 2
        res = forward_backward(m, obs, constraints=cons)
        self.assertTrue(res.feasible)
        self.assertEqual(res.posteriors[1], (0.0, 0.0, 1.0))
        bf = brute_force(m, obs, constraints=cons)
        self.assertAlmostEqual(res.log_evidence, bf.log_evidence, places=9)
        for got, want in zip(res.posteriors, bf.posteriors):
            for g, w in zip(got, want):
                self.assertAlmostEqual(g, w, places=9)

    def test_constraint_conflicting_with_emission_is_infeasible(self):
        m = HMM(2, (0.5, 0.5), ((0.5, 0.5), (0.5, 0.5)),
                ({"a": 1.0}, {"b": 1.0}))
        res = forward_backward(m, ("a",), constraints=({1},))
        self.assertFalse(res.feasible)
        self.assertIsNone(res.posteriors)

    def test_empty_constraint_set_is_usage_error(self):
        m = make_model()
        with self.assertRaises(ValueError):
            forward_backward(m, ("a",), constraints=(set(),))

    def test_piecewise_transitions_match_brute_force(self):
        m = make_three_state_model()
        obs = ("x", "y", "z", "x")
        seg = [
            ((0.9, 0.1, 0.0), (0.0, 0.9, 0.1), (0.0, 0.0, 1.0)),
            ((0.3, 0.3, 0.4), (0.4, 0.3, 0.3), (0.3, 0.4, 0.3)),
            ((0.6, 0.3, 0.1), (0.2, 0.5, 0.3), (0.3, 0.3, 0.4)),
        ]
        res = forward_backward(m, obs, transitions=seg)
        bf = brute_force(m, obs, transitions=seg)
        self.assertAlmostEqual(res.log_evidence, bf.log_evidence, places=9)
        for got, want in zip(res.posteriors, bf.posteriors):
            for g, w in zip(got, want):
                self.assertAlmostEqual(g, w, places=9)
        self.assertEqual(res.viterbi_path, bf.best_path)

    def test_piecewise_wrong_length_rejected(self):
        m = make_model()
        with self.assertRaises(ValueError):
            forward_backward(m, ("a", "b", "a"),
                             transitions=[m.transition])  # need 2


class TestMapVsViterbi(unittest.TestCase):
    def test_pointwise_map_differs_from_viterbi(self):
        # Constructed so the per-time MAP states are (0, 1) while the single
        # best joint path is (1, 1).
        m = HMM(
            n_states=2,
            initial=(0.75, 0.25),
            transition=((0.9, 0.1), (0.1, 0.9)),
            emission=({"x": 0.5, "y": 0.5}, {"x": 0.9, "y": 0.1}),
        )
        res = forward_backward(m, ("x", "x"))
        pointwise_map = tuple(
            max(range(2), key=lambda j: row[j]) for row in res.posteriors
        )
        self.assertEqual(pointwise_map, (0, 1))
        self.assertEqual(res.viterbi_path, (1, 1))
        self.assertNotEqual(pointwise_map, res.viterbi_path)
        # The MAP concatenation is a *worse* joint path than Viterbi's.
        from hmm import path_log_joint
        lj_map = path_log_joint(m, ("x", "x"), pointwise_map)
        self.assertLess(lj_map, res.viterbi_logprob)


if __name__ == "__main__":
    unittest.main()
