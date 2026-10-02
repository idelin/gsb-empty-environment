import math
import unittest

from hmm import (
    NEG_INF,
    HMM,
    PiecewiseHMM,
    accumulate_log_probability,
    exact_evidence,
    exact_posteriors,
    exact_topk,
    exact_viterbi,
    forward_backward,
    pointwise_map,
    topk_paths,
    viterbi,
)


def weather_model():
    return HMM(
        initial=[0.6, 0.4],
        transition=[[0.7, 0.3], [0.4, 0.6]],
        emission=[
            {"walk": 0.6, "shop": 0.3, "clean": 0.1},
            {"walk": 0.1, "shop": 0.4, "clean": 0.5},
        ],
    )


def assert_matrix_close(testcase, got, expected, places=9):
    testcase.assertEqual(len(got), len(expected))
    for grow, erow in zip(got, expected):
        testcase.assertEqual(len(grow), len(erow))
        for g, e in zip(grow, erow):
            testcase.assertAlmostEqual(g, e, places=places)


class TestAgainstEnumeration(unittest.TestCase):
    """Dynamic programming vs brute-force enumeration on a small HMM."""

    def setUp(self):
        self.model = weather_model()
        self.obs = ["walk", "shop", "clean", "walk", None, "shop"]

    def test_log_evidence_matches_enumeration(self):
        result = forward_backward(self.model, self.obs)
        self.assertTrue(result.feasible)
        exact = exact_evidence(self.model, self.obs)
        self.assertAlmostEqual(result.log_evidence, math.log(exact), places=9)

    def test_posteriors_match_enumeration(self):
        result = forward_backward(self.model, self.obs)
        exact = exact_posteriors(self.model, self.obs)
        assert_matrix_close(self, result.posteriors, exact)

    def test_posterior_rows_are_normalised(self):
        result = forward_backward(self.model, self.obs)
        for row in result.posteriors:
            self.assertAlmostEqual(sum(row), 1.0, places=9)

    def test_viterbi_matches_enumeration(self):
        result = viterbi(self.model, self.obs)
        path, prob = exact_viterbi(self.model, self.obs)
        self.assertEqual(tuple(result.path), path)
        self.assertAlmostEqual(result.log_prob, math.log(prob), places=9)

    def test_topk_matches_enumeration(self):
        k = 5
        got = topk_paths(self.model, self.obs, k)
        exact = exact_topk(self.model, self.obs, k)
        self.assertEqual(len(got), k)
        for scored, (path, prob) in zip(got, exact):
            self.assertEqual(tuple(scored.path), path)
            self.assertAlmostEqual(scored.log_prob, math.log(prob), places=9)

    def test_topk_paths_are_distinct_and_sorted(self):
        got = topk_paths(self.model, self.obs, 10)
        paths = [tuple(s.path) for s in got]
        self.assertEqual(len(paths), len(set(paths)))
        logps = [s.log_prob for s in got]
        self.assertEqual(logps, sorted(logps, reverse=True))

    def test_accumulation_is_order_independent(self):
        got = topk_paths(self.model, self.obs, 8)
        logps = [s.log_prob for s in got]
        forward = accumulate_log_probability(logps)
        backward = accumulate_log_probability(list(reversed(logps)))
        self.assertAlmostEqual(forward, backward, places=12)
        exact_mass = sum(p for _, p in exact_topk(self.model, self.obs, 8))
        self.assertAlmostEqual(forward, math.log(exact_mass), places=9)

    def test_constrained_paths_match_constrained_enumeration(self):
        allowed = [{0}, None, {1}, None, None, {0, 1}]
        result = forward_backward(self.model, self.obs, allowed)
        exact = exact_posteriors(self.model, self.obs, allowed)
        assert_matrix_close(self, result.posteriors, exact)
        self.assertAlmostEqual(result.posteriors[0][0], 1.0)
        self.assertAlmostEqual(result.posteriors[2][1], 1.0)
        vit = viterbi(self.model, self.obs, allowed)
        path, _ = exact_viterbi(self.model, self.obs, allowed)
        self.assertEqual(tuple(vit.path), path)

    def test_impossible_constraint_is_infeasible(self):
        # an empty allowed-state set makes the sequence impossible
        allowed = [set()] + [None] * 5
        result = forward_backward(self.model, self.obs, allowed)
        self.assertFalse(result.feasible)
        self.assertIsNone(result.posteriors)
        self.assertEqual(result.log_evidence, NEG_INF)
        vit = viterbi(self.model, self.obs, allowed)
        self.assertFalse(vit.feasible)
        self.assertIsNone(vit.path)


class TestPiecewiseInference(unittest.TestCase):
    def test_piecewise_matches_enumeration(self):
        model = PiecewiseHMM(
            initial=[0.5, 0.5],
            transitions=[
                [[0.9, 0.1], [0.2, 0.8]],
                [[0.3, 0.7], [0.6, 0.4]],
            ],
            boundaries=[3],
            emission=[{"x": 0.8, "y": 0.2}, {"x": 0.3, "y": 0.7}],
        )
        obs = ["x", "y", "x", "x", "y"]
        result = forward_backward(model, obs)
        self.assertAlmostEqual(
            result.log_evidence, math.log(exact_evidence(model, obs)), places=9
        )
        assert_matrix_close(self, result.posteriors, exact_posteriors(model, obs))
        vit = viterbi(model, obs)
        path, prob = exact_viterbi(model, obs)
        self.assertEqual(tuple(vit.path), path)
        self.assertAlmostEqual(vit.log_prob, math.log(prob), places=9)


class TestScaling(unittest.TestCase):
    def test_long_sequence_does_not_underflow(self):
        model = weather_model()
        T = 20000
        obs = ["walk", "shop", "clean"] * (T // 3) + ["walk"] * (T - 3 * (T // 3))
        result = forward_backward(model, obs)
        self.assertTrue(result.feasible)
        self.assertTrue(math.isfinite(result.log_evidence))
        self.assertLess(result.log_evidence, 0.0)
        for row in result.posteriors[:5] + result.posteriors[-5:]:
            self.assertAlmostEqual(sum(row), 1.0, places=9)
        vit = viterbi(model, obs)
        self.assertTrue(vit.feasible)
        self.assertEqual(len(vit.path), T)


class TestDegenerateCases(unittest.TestCase):
    def test_zero_probability_sequence_is_explicitly_infeasible(self):
        model = HMM(
            initial=[1.0, 0.0],
            transition=[[0.5, 0.5], [0.5, 0.5]],
            emission=[{"a": 1.0}, {"b": 1.0}],
        )
        result = forward_backward(model, ["b"])
        self.assertFalse(result.feasible)
        self.assertIsNone(result.posteriors)  # no renormalised pseudo-posterior
        self.assertEqual(result.log_evidence, NEG_INF)
        self.assertEqual(exact_evidence(model, ["b"]), 0.0)
        self.assertIsNone(exact_posteriors(model, ["b"]))

    def test_impossible_observation_means_no_path(self):
        model = weather_model()
        result = viterbi(model, ["walk", "teleport"])
        self.assertFalse(result.feasible)
        self.assertIsNone(result.path)
        self.assertEqual(result.log_prob, NEG_INF)
        self.assertEqual(topk_paths(model, ["walk", "teleport"], 3), [])

    def test_empty_sequence_conventions(self):
        model = weather_model()
        fb = forward_backward(model, [])
        self.assertTrue(fb.feasible)
        self.assertEqual(fb.log_evidence, 0.0)  # evidence 1.0 by convention
        self.assertEqual(fb.posteriors, [])
        vit = viterbi(model, [])
        self.assertTrue(vit.feasible)
        self.assertEqual(vit.path, [])
        self.assertEqual(vit.log_prob, 0.0)
        top = topk_paths(model, [], 3)
        self.assertEqual(len(top), 1)
        self.assertEqual(top[0].path, [])
        self.assertEqual(top[0].log_prob, 0.0)

    def test_viterbi_path_is_not_pointwise_map_concatenation(self):
        # Constructed so the per-time MAP states form a worse path than the
        # Viterbi path: MAP concat = [0, 1, 1] (p=0.24), Viterbi = [0, 1, 0]
        # (p=0.36).
        model = HMM(
            initial=[0.6, 0.4],
            transition=[[0.0, 1.0], [0.6, 0.4]],
            emission=[{"a": 1.0}, {"a": 1.0}],
        )
        obs = ["a", "a", "a"]
        fb = forward_backward(model, obs)
        map_path = pointwise_map(fb.posteriors)
        vit = viterbi(model, obs)
        self.assertEqual(map_path, [0, 1, 1])
        self.assertEqual(vit.path, [0, 1, 0])
        self.assertNotEqual(map_path, vit.path)
        self.assertGreater(vit.log_prob, math.log(0.24))


if __name__ == "__main__":
    unittest.main()
