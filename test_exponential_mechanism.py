"""unittest suite for exponential_mechanism (synthetic data only)."""

import math
import unittest
from decimal import Decimal

from exponential_mechanism import (
    Candidate,
    Constraints,
    ExponentialMechanism,
    InfeasibleError,
)


def make(candidates, budget, epsilon=1.0, sensitivity=1.0, **kw):
    quotas = kw.pop("quotas", {})
    mutex = kw.pop("mutex", frozenset())
    return ExponentialMechanism(
        [Candidate(*c) if isinstance(c, tuple) else c for c in candidates],
        Constraints(budget=budget, group_quotas=quotas, mutex_pairs=mutex),
        epsilon=epsilon,
        sensitivity=sensitivity,
        **kw,
    )


class TestValidation(unittest.TestCase):
    def test_duplicate_ids_rejected(self):
        with self.assertRaises(ValueError):
            make([Candidate("a", 1), Candidate("a", 2)], budget=1)

    def test_non_integer_score_rejected(self):
        with self.assertRaises(TypeError):
            Candidate("a", 1.5)
        with self.assertRaises(TypeError):
            Candidate("a", True)

    def test_invalid_parameters_rejected(self):
        for bad in (-1.0, math.nan, math.inf):
            with self.assertRaises(ValueError):
                make([("a", 1)], budget=1, epsilon=bad)
        with self.assertRaises(ValueError):
            make([("a", 1)], budget=1, sensitivity=-1.0)
        with self.assertRaises(ValueError):
            make([("a", 1)], budget=1, sensitivity=math.nan)
        with self.assertRaises(ValueError):
            make([("a", 1)], budget=1, sensitivity=math.inf)

    def test_mutex_unknown_id_rejected(self):
        with self.assertRaises(ValueError):
            make([("a", 1), ("b", 2)], budget=1, mutex={frozenset({"a", "zz"})})


class TestUniformAndDeterministicRules(unittest.TestCase):
    def test_equal_scores_give_uniform_distribution(self):
        mech = make([("a", 7), ("b", 7), ("c", 7), ("d", 7)], budget=2)
        probs = mech.probabilities()
        self.assertEqual(len(probs), 6)  # C(4,2)
        for p in probs.values():
            self.assertAlmostEqual(p, 1.0 / 6.0, places=15)

    def test_epsilon_zero_gives_uniform_distribution(self):
        mech = make([("a", 1), ("b", 99), ("c", -5)], budget=1, epsilon=0.0)
        for p in mech.probabilities().values():
            self.assertAlmostEqual(p, 1.0 / 3.0, places=15)

    def test_sensitivity_zero_uses_explicit_argmax_rule(self):
        # No ZeroDivisionError; unique max-utility subset gets probability 1.
        mech = make([("a", 1), ("b", 2), ("c", 3)], budget=1, sensitivity=0.0)
        probs = mech.probabilities()
        self.assertEqual(probs[("c",)], 1.0)
        self.assertEqual(probs[("a",)], 0.0)

    def test_sensitivity_zero_tie_is_uniform_over_argmax(self):
        mech = make([("a", 5), ("b", 5), ("c", 1)], budget=1, sensitivity=0.0)
        probs = mech.probabilities()
        self.assertEqual(probs[("a",)], 0.5)
        self.assertEqual(probs[("b",)], 0.5)
        self.assertEqual(probs[("c",)], 0.0)


class TestConstraints(unittest.TestCase):
    def test_group_quotas_respected(self):
        cands = [
            Candidate("a1", 3, "A"), Candidate("a2", 2, "A"),
            Candidate("b1", 1, "B"),
        ]
        mech = make(cands, budget=2, quotas={"A": 1})
        for subset in mech.subsets:
            groups = [c[:1] for c in subset]
            self.assertLessEqual(groups.count("A"), 1)
        self.assertNotIn(("a1", "a2"), mech.subsets)

    def test_mutex_pairs_respected(self):
        mech = make(
            [("a", 1), ("b", 2), ("c", 3)], budget=2,
            mutex={frozenset({"a", "b"})},
        )
        self.assertNotIn(("a", "b"), mech.subsets)
        self.assertEqual(len(mech.subsets), 2)

    def test_budget_exceeds_candidates_raises_not_truncates(self):
        with self.assertRaises(InfeasibleError):
            make([("a", 1), ("b", 2)], budget=5)

    def test_all_candidates_illegal_raises(self):
        # Every pair is mutually exclusive and budget is 2.
        with self.assertRaises(InfeasibleError):
            make(
                [("a", 1), ("b", 2)], budget=2,
                mutex={frozenset({"a", "b"})},
            )

    def test_zero_quotas_make_domain_infeasible(self):
        with self.assertRaises(InfeasibleError):
            make(
                [Candidate("a", 1, "A"), Candidate("b", 2, "B")],
                budget=1, quotas={"A": 0, "B": 0},
            )

    def test_oversized_domain_refused_instead_of_truncated(self):
        cands = [Candidate(f"c{i:02d}", i) for i in range(20)]
        with self.assertRaises(InfeasibleError) as ctx:
            make(cands, budget=10, max_domain_size=1000)
        self.assertIn("refusing to truncate", str(ctx.exception))


class TestNumericalStability(unittest.TestCase):
    def test_extreme_scores_do_not_overflow(self):
        mech = make(
            [("lo", -10**9), ("mid", 0), ("hi", 10**9)],
            budget=1, epsilon=10.0, sensitivity=1.0,
        )
        probs = mech.probabilities()
        self.assertEqual(probs[("hi",)], 1.0)
        self.assertEqual(probs[("lo",)], 0.0)
        self.assertAlmostEqual(sum(probs.values()), 1.0, places=15)

    def test_huge_epsilon_falls_back_to_argmax(self):
        # epsilon / (2 * sensitivity) overflows to +inf: the limit of the
        # distribution is uniform over the max-utility subsets.
        mech = make(
            [("a", -3), ("b", -2), ("c", -1)],
            budget=1, epsilon=1e308, sensitivity=1e-300,
        )
        probs = mech.probabilities()
        self.assertEqual(probs[("c",)], 1.0)

    def test_probabilities_sum_to_one(self):
        mech = make(
            [("a", 3), ("b", -7), ("c", 42), ("d", 0), ("e", 11)],
            budget=3, epsilon=0.37, sensitivity=2.5,
        )
        self.assertAlmostEqual(sum(mech.probabilities().values()), 1.0, places=12)

    def test_float_probabilities_match_exact_reference(self):
        mech = make(
            [("a", 0), ("b", 1), ("c", 2), ("d", 3)],
            budget=2, epsilon=0.7, sensitivity=1.5,
        )
        exact = mech.exact_probabilities(precision=50)
        self.assertTrue(all(isinstance(v, Decimal) for v in exact.values()))
        err = mech.max_probability_error()
        self.assertLess(err, 1e-12)
        self.assertAlmostEqual(
            float(sum(exact.values())), 1.0, places=40
        )


class TestSamplingEvidence(unittest.TestCase):
    def setUp(self):
        self.mech = make(
            [("a", 0), ("b", 1), ("c", 2), ("d", 3)],
            budget=2, epsilon=1.0, sensitivity=1.0,
        )

    def test_record_fields_are_consistent(self):
        rec = self.mech.sample(seed=42)
        probs = self.mech.probabilities()
        self.assertEqual(rec.domain_size, len(self.mech.subsets))
        self.assertTrue(0.0 <= rec.draw < 1.0)
        self.assertEqual(rec.subset_probability, probs[rec.subset])
        self.assertLessEqual(rec.cumulative_before, rec.draw)
        self.assertLess(rec.draw, rec.cumulative_after + 1e-15)
        self.assertAlmostEqual(
            rec.cumulative_after - rec.cumulative_before,
            rec.subset_probability,
            places=15,
        )

    def test_sampling_is_reproducible_and_verifiable(self):
        rec1 = self.mech.sample(seed=123)
        rec2 = self.mech.sample(seed=123)
        self.assertEqual(rec1, rec2)
        self.assertTrue(self.mech.verify(rec1))

    def test_unseeded_draw_still_returns_auditable_seed(self):
        rec = self.mech.sample()
        self.assertIsInstance(rec.seed, int)
        self.assertTrue(self.mech.verify(rec))

    def test_empirical_frequencies_track_probabilities(self):
        # Statistical sanity check only -- NOT a formal privacy proof.
        mech = make([("a", 0), ("b", 1)], budget=1,
                    epsilon=math.log(3.0), sensitivity=0.5)
        # weights 1 : 3  ->  P(b) = 0.75
        trials = 8000
        counts = {("a",): 0, ("b",): 0}
        for i in range(trials):
            counts[mech.sample(seed=10_000 + i).subset] += 1
        expected = 0.75 * trials
        sigma = math.sqrt(trials * 0.75 * 0.25)
        self.assertLess(abs(counts[("b",)] - expected), 6 * sigma)


if __name__ == "__main__":
    unittest.main()
