"""Unittest suite for the subset exponential mechanism (synthetic data only).

The frequency-based test is a statistical sanity experiment with a fixed
seed; it is not a formal privacy proof.
"""

import math
import unittest
from decimal import Decimal

from expmech import (
    Candidate,
    Constraints,
    ExponentialMechanism,
    InfeasibleError,
    enumerate_legal_subsets,
    utility_sensitivity_fixed_size,
)


def make_mechanism(scores, subset_size=1, quotas=None, mutex=(), epsilon=1.0,
                   sensitivity=None, groups=None):
    groups = groups or ["g"] * len(scores)
    candidates = [Candidate(f"c{i}", s, groups[i]) for i, s in enumerate(scores)]
    constraints = Constraints(
        subset_size=subset_size,
        group_quotas=quotas or {},
        mutex_pairs=frozenset(frozenset(p) for p in mutex),
    )
    if sensitivity is None:
        sensitivity = float(utility_sensitivity_fixed_size(candidates)) or 1.0
    return ExponentialMechanism(candidates, constraints, epsilon, sensitivity)


class TestUniformAndDegenerateRules(unittest.TestCase):
    def test_all_equal_scores_give_uniform(self):
        mech = make_mechanism([4, 4, 4, 4], subset_size=2, epsilon=3.0)
        probs = mech.probabilities()
        self.assertEqual(len(probs), 6)  # C(4,2)
        for p in probs:
            self.assertAlmostEqual(p, 1.0 / 6.0, places=15)

    def test_zero_epsilon_gives_uniform(self):
        mech = make_mechanism([1, 9, -5, 7], subset_size=2, epsilon=0.0)
        for p in mech.probabilities():
            self.assertAlmostEqual(p, 1.0 / 6.0, places=15)

    def test_zero_sensitivity_uses_explicit_uniform_rule(self):
        # Deterministic documented rule: sensitivity 0 -> uniform, no div-by-zero.
        mech = make_mechanism([1, 100, -40], subset_size=1, epsilon=5.0,
                              sensitivity=0.0)
        probs = mech.probabilities()
        self.assertEqual(len(probs), 3)
        for p in probs:
            self.assertAlmostEqual(p, 1.0 / 3.0, places=15)
        ref = mech.probabilities_decimal()
        for r in ref:
            self.assertAlmostEqual(float(r), 1.0 / 3.0, places=40)

    def test_empty_subset_size_zero_is_single_legal_subset(self):
        mech = make_mechanism([1, 2, 3], subset_size=0)
        self.assertEqual(len(mech.subsets), 1)
        self.assertEqual(mech.subsets[0], ())
        self.assertEqual(mech.probabilities(), [1.0])


class TestValidation(unittest.TestCase):
    def test_duplicate_ids_rejected(self):
        candidates = [Candidate("x", 1), Candidate("x", 2)]
        with self.assertRaises(ValueError):
            ExponentialMechanism(candidates, Constraints(1), 1.0, 1.0)

    def test_non_integer_score_rejected(self):
        with self.assertRaises(ValueError):
            Candidate("bad", 1.5)
        with self.assertRaises(ValueError):
            Candidate("bool", True)

    def test_negative_epsilon_or_sensitivity_rejected(self):
        candidates = [Candidate("a", 1), Candidate("b", 2)]
        with self.assertRaises(ValueError):
            ExponentialMechanism(candidates, Constraints(1), -0.1, 1.0)
        with self.assertRaises(ValueError):
            ExponentialMechanism(candidates, Constraints(1), 1.0, -1.0)
        with self.assertRaises(ValueError):
            ExponentialMechanism(candidates, Constraints(1), math.inf, 1.0)

    def test_mutex_referencing_unknown_id_rejected(self):
        candidates = [Candidate("a", 1), Candidate("b", 2)]
        constraints = Constraints(1, mutex_pairs=frozenset({frozenset({"a", "zz"})}))
        with self.assertRaises(ValueError):
            ExponentialMechanism(candidates, constraints, 1.0, 1.0)

    def test_bad_quota_rejected(self):
        with self.assertRaises(ValueError):
            Constraints(1, group_quotas={"g": -1})


class TestFeasibility(unittest.TestCase):
    def test_all_candidates_illegal_raises(self):
        # Quota 0 for the only group rules out every candidate.
        candidates = [Candidate("a", 1, "g"), Candidate("b", 2, "g")]
        constraints = Constraints(1, group_quotas={"g": 0})
        with self.assertRaises(InfeasibleError):
            ExponentialMechanism(candidates, constraints, 1.0, 1.0)

    def test_budget_larger_than_candidate_pool_raises(self):
        candidates = [Candidate("a", 1), Candidate("b", 2)]
        with self.assertRaises(InfeasibleError):
            ExponentialMechanism(candidates, Constraints(3), 1.0, 1.0)

    def test_budget_exceeding_quota_capacity_raises_not_truncates(self):
        # 3 candidates in one group, quota 2, budget 3: infeasible.
        # The mechanism must raise, never silently drop a candidate and
        # keep sampling as if the original distribution still applied.
        candidates = [Candidate(f"c{i}", i, "g") for i in range(3)]
        constraints = Constraints(3, group_quotas={"g": 2})
        with self.assertRaises(InfeasibleError):
            ExponentialMechanism(candidates, constraints, 1.0, 1.0)

    def test_fully_mutexed_domain_raises(self):
        candidates = [Candidate("a", 1), Candidate("b", 2)]
        constraints = Constraints(2, mutex_pairs=frozenset({frozenset({"a", "b"})}))
        with self.assertRaises(InfeasibleError):
            ExponentialMechanism(candidates, constraints, 1.0, 1.0)


class TestConstraints(unittest.TestCase):
    def test_quotas_and_mutex_shape_the_domain(self):
        candidates = [
            Candidate("a1", 1, "A"), Candidate("a2", 2, "A"),
            Candidate("b1", 3, "B"), Candidate("b2", 4, "B"),
        ]
        constraints = Constraints(
            subset_size=2,
            group_quotas={"A": 1, "B": 2},
            mutex_pairs=frozenset({frozenset({"a1", "b1"})}),
        )
        subsets = enumerate_legal_subsets(candidates, constraints)
        id_sets = {tuple(sorted(c.cid for c in s)) for s in subsets}
        # All C(4,2)=6 pairs minus ("a1","a2") [quota A<=1] minus ("a1","b1") [mutex].
        self.assertEqual(
            id_sets,
            {("a1", "b2"), ("a2", "b1"), ("a2", "b2"), ("b1", "b2")},
        )


class TestNumerics(unittest.TestCase):
    def test_extreme_scores_no_overflow(self):
        # Naive exp(eps*u/(2*sens)) = exp(5e5) would overflow float64.
        scores = [10**6, -(10**6), 0, 123456]
        mech = make_mechanism(scores, subset_size=1, epsilon=1.0, sensitivity=1.0)
        probs = mech.probabilities()
        self.assertTrue(all(math.isfinite(p) for p in probs))
        self.assertAlmostEqual(sum(probs), 1.0, places=15)
        # The top-score candidate dominates completely at this scale.
        self.assertAlmostEqual(probs[0], 1.0, places=15)
        self.assertEqual(probs[1], 0.0)  # underflow to exactly 0 is fine here

    def test_extreme_scores_moderate_ratio_exact_reference(self):
        scores = [10**9, -(10**9), 5 * 10**8]
        sens = float(utility_sensitivity_fixed_size(
            [Candidate(f"c{i}", s) for i, s in enumerate(scores)]))
        mech = make_mechanism(scores, subset_size=1, epsilon=2.0, sensitivity=sens)
        err = mech.numerical_error()
        self.assertLess(err["max_rel_error"], 1e-12)
        probs = mech.probabilities()
        self.assertAlmostEqual(sum(probs), 1.0, places=15)

    def test_float64_matches_decimal_reference_on_small_domain(self):
        mech = make_mechanism([7, 3, -2, 5, 5, 9, 0, 4], subset_size=3,
                              quotas={"g": 3}, epsilon=1.2)
        err = mech.numerical_error()
        self.assertLess(err["max_abs_error"], 1e-12)
        self.assertLess(err["max_rel_error"], 1e-12)

    def test_decimal_reference_is_normalised(self):
        mech = make_mechanism([1, 2, 3, 4], subset_size=2, epsilon=0.7)
        total = sum(mech.probabilities_decimal(), Decimal(0))
        self.assertAlmostEqual(float(total), 1.0, places=40)

    def test_probabilities_sum_to_one(self):
        mech = make_mechanism([3, 1, 4, 1, 5, 9, 2, 6], subset_size=3, epsilon=2.5)
        self.assertAlmostEqual(math.fsum(mech.probabilities()), 1.0, places=15)

    def test_higher_utility_gets_higher_probability(self):
        mech = make_mechanism([0, 1, 2], subset_size=1, epsilon=1.0, sensitivity=2.0)
        probs = mech.probabilities()
        self.assertLess(probs[0], probs[1])
        self.assertLess(probs[1], probs[2])


class TestSamplingEvidence(unittest.TestCase):
    def test_same_seed_reproduces_draws(self):
        mech = make_mechanism([1, 2, 3, 4], subset_size=2, epsilon=1.0)
        e1 = mech.sample(n=10, seed=42)
        e2 = mech.sample(n=10, seed=42)
        self.assertEqual(e1, e2)

    def test_evidence_replay_verifies(self):
        mech = make_mechanism([1, 2, 3, 4], subset_size=2, epsilon=1.0)
        evidence = mech.sample(n=25, seed=1234)
        self.assertTrue(evidence.replay())

    def test_tampered_evidence_fails_replay(self):
        mech = make_mechanism([1, 2, 3, 4], subset_size=2, epsilon=1.0)
        evidence = mech.sample(n=5, seed=99)
        bad = evidence.draws[0]
        tampered = type(bad)(
            draw_index=bad.draw_index,
            uniform=(bad.uniform + 0.5) % 1.0,
            subset_index=bad.subset_index,
            subset_ids=bad.subset_ids,
            subset_probability=bad.subset_probability,
        )
        broken = type(evidence)(
            seed=evidence.seed,
            epsilon=evidence.epsilon,
            sensitivity=evidence.sensitivity,
            probabilities=evidence.probabilities,
            subset_ids=evidence.subset_ids,
            draws=(tampered,) + evidence.draws[1:],
        )
        self.assertFalse(broken.replay())

    def test_every_drawn_subset_is_legal(self):
        mech = make_mechanism(
            [1, 2, 3, 4], subset_size=2,
            quotas={"g": 2}, epsilon=1.0,
        )
        evidence = mech.sample(n=50, seed=5)
        legal = {tuple(sorted(c.cid for c in s)) for s in mech.subsets}
        for d in evidence.draws:
            self.assertIn(tuple(sorted(d.subset_ids)), legal)

    def test_invalid_sample_arguments_rejected(self):
        mech = make_mechanism([1, 2], subset_size=1)
        with self.assertRaises(ValueError):
            mech.sample(n=0)
        with self.assertRaises(ValueError):
            mech.sample(n=1.5)

    def test_frequency_matches_distribution_statistically(self):
        # Statistical sanity experiment only -- NOT a formal privacy proof.
        mech = make_mechanism([0, 1, 2, 3], subset_size=1, epsilon=1.0,
                              sensitivity=3.0)
        n = 40000
        evidence = mech.sample(n=n, seed=2026)
        probs = evidence.probabilities
        counts = [0] * len(probs)
        for d in evidence.draws:
            counts[d.subset_index] += 1
        for i, p in enumerate(probs):
            self.assertAlmostEqual(counts[i] / n, p, delta=0.02)


if __name__ == "__main__":
    unittest.main()
