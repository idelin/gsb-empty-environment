import math
import unittest

from hmm import (
    HMM,
    NEG_INF,
    accumulate_path_probabilities,
    brute_force,
    forward_backward,
    path_log_joint,
    rank_topk_paths,
)


def make_model():
    return HMM(
        n_states=2,
        initial=(0.6, 0.4),
        transition=((0.7, 0.3), (0.4, 0.6)),
        emission=({"a": 0.5, "b": 0.5}, {"a": 0.1, "b": 0.9}),
    )


class TestRanking(unittest.TestCase):
    def test_top1_matches_viterbi(self):
        m = make_model()
        obs = ("a", "b", "b", "a", "b")
        ranked = rank_topk_paths(m, obs, 1)
        res = forward_backward(m, obs)
        self.assertEqual(len(ranked), 1)
        self.assertEqual(ranked[0].path, res.viterbi_path)
        self.assertAlmostEqual(ranked[0].log_joint, res.viterbi_logprob, places=9)

    def test_topk_matches_brute_force(self):
        m = make_model()
        obs = ("a", "b", "a", "b")
        k = 7
        ranked = rank_topk_paths(m, obs, k)
        bf = brute_force(m, obs)
        want = bf.path_log_joints[:k]
        self.assertEqual(len(ranked), k)
        # Paths are distinct and scores match enumeration (order-insensitive
        # per score to stay robust to ties).
        paths = [rp.path for rp in ranked]
        self.assertEqual(len(set(paths)), k)
        got_scores = sorted(rp.log_joint for rp in ranked)
        want_scores = sorted(lj for _, lj in want)
        for g, w in zip(got_scores, want_scores):
            self.assertAlmostEqual(g, w, places=9)
        self.assertEqual({rp.path for rp in ranked}, {p for p, _ in want})
        # Ranks are non-increasing in score.
        for a, b in zip(ranked, ranked[1:]):
            self.assertGreaterEqual(a.log_joint, b.log_joint)

    def test_fewer_paths_than_k(self):
        m = HMM(2, (1.0, 0.0), ((1.0, 0.0), (0.0, 1.0)),
                ({"a": 1.0}, {"b": 1.0}))
        ranked = rank_topk_paths(m, ("a", "a"), 5)
        self.assertEqual(len(ranked), 1)  # only one reachable path
        self.assertEqual(ranked[0].path, (0, 0))

    def test_infeasible_sequence_ranks_nothing(self):
        m = make_model()
        self.assertEqual(rank_topk_paths(m, ("a", "q"), 3), [])

    def test_empty_sequence(self):
        m = make_model()
        ranked = rank_topk_paths(m, (), 3)
        self.assertEqual(ranked[0].path, ())
        self.assertEqual(ranked[0].log_joint, 0.0)

    def test_bad_k_rejected(self):
        m = make_model()
        with self.assertRaises(ValueError):
            rank_topk_paths(m, ("a",), 0)


class TestAccumulation(unittest.TestCase):
    def test_accumulation_is_independent_of_ranking_scores(self):
        m = make_model()
        obs = ("a", "b", "a")
        ranked = rank_topk_paths(m, obs, 4)
        acc = accumulate_path_probabilities(m, obs, ranked)
        self.assertEqual(acc.kind, "exact")
        self.assertTrue(acc.feasible)
        # Probabilities recomputed from scratch match the DP scores.
        for entry, rp in zip(acc.entries, ranked):
            self.assertAlmostEqual(entry.log_joint, rp.log_joint, places=9)
            self.assertAlmostEqual(
                entry.joint_prob, math.exp(entry.log_joint), places=12
            )
        # Cumulative conditional mass is monotone and bounded by 1.
        last = 0.0
        for entry in acc.entries:
            self.assertGreaterEqual(entry.cumulative_conditional, last)
            last = entry.cumulative_conditional
        self.assertLessEqual(last, 1.0 + 1e-12)
        self.assertAlmostEqual(
            acc.covered_conditional_mass + acc.residual_conditional_mass, 1.0,
            places=12,
        )

    def test_all_paths_cover_full_mass(self):
        m = make_model()
        obs = ("a", "b", "a")
        n_paths = 2 ** len(obs)
        ranked = rank_topk_paths(m, obs, n_paths)
        self.assertEqual(len(ranked), n_paths)
        acc = accumulate_path_probabilities(m, obs, ranked)
        self.assertAlmostEqual(acc.covered_conditional_mass, 1.0, places=9)
        self.assertAlmostEqual(acc.residual_conditional_mass, 0.0, places=9)

    def test_conditional_probs_match_brute_force(self):
        m = make_model()
        obs = ("b", "a", "b", "a")
        ranked = rank_topk_paths(m, obs, 5)
        acc = accumulate_path_probabilities(m, obs, ranked)
        bf = brute_force(m, obs)
        for entry in acc.entries:
            lj = dict((p, lj) for p, lj in bf.path_log_joints)[entry.path]
            self.assertAlmostEqual(
                entry.conditional_prob, math.exp(lj - bf.log_evidence), places=12
            )

    def test_infeasible_accumulation_has_no_conditionals(self):
        m = make_model()
        acc = accumulate_path_probabilities(m, ("a", "q"), [])
        self.assertFalse(acc.feasible)
        self.assertEqual(acc.log_evidence, NEG_INF)
        self.assertIsNone(acc.covered_conditional_mass)
        self.assertIsNone(acc.residual_conditional_mass)

    def test_path_log_joint_validates_length(self):
        m = make_model()
        with self.assertRaises(ValueError):
            path_log_joint(m, ("a",), (0, 1))


if __name__ == "__main__":
    unittest.main()
