"""Test suite for the query-learning experiment library."""
from __future__ import annotations

import os
import random
import tempfile
import unittest

from dfa import DFA
from learner import (CheckpointMismatch, ObservationTableLearner,
                     STATUS_BUDGET_UNDECIDED, STATUS_EQUIVALENT_IN_DOMAIN)
from machines import (ends_with_dfa, exact_a_run_dfa,
                      no_double_letter_dfa, random_dfa)
from oracle import BoundedEquivalenceOracle, MembershipOracle
from verifier import (BruteForceCheckedOracle, brute_force_counterexample,
                      domain_equivalent, iter_words)

AB = ("a", "b")


def make_learner(hidden, alphabet, max_len, budget=None):
    mq = MembershipOracle(hidden)
    oracle = BoundedEquivalenceOracle(hidden, alphabet, max_len)
    learner = ObservationTableLearner(alphabet, max_len, mq, oracle.version,
                                      budget=budget)
    return learner, oracle, mq


class TestProductOracle(unittest.TestCase):
    def test_matches_brute_force_on_random_dfas(self):
        rng = random.Random(20260923)
        for _ in range(60):
            hidden = random_dfa(rng, rng.randint(1, 5), AB)
            hyp = random_dfa(rng, rng.randint(1, 5), AB)
            oracle = BoundedEquivalenceOracle(hidden, AB, 4)
            ce = oracle.find_counterexample(hyp)
            expected = brute_force_counterexample(hidden, hyp, AB, 4)
            self.assertEqual(ce, expected)

    def test_shortest_then_lexicographic(self):
        # hidden accepts exactly {"ab"}; all-reject hypothesis.
        hidden = ends_with_dfa("ab", AB)
        hyp = DFA(1, AB, 0, {(0, "a"): 0, (0, "b"): 0}, set())
        oracle = BoundedEquivalenceOracle(hidden, AB, 5)
        self.assertEqual(oracle.find_counterexample(hyp), "ab")

    def test_empty_string_difference(self):
        hidden = exact_a_run_dfa(0, AB)  # accepts only ""
        hyp = DFA(1, AB, 0, {(0, "a"): 0, (0, "b"): 0}, set())
        oracle = BoundedEquivalenceOracle(hidden, AB, 3)
        self.assertEqual(oracle.find_counterexample(hyp), "")
        self.assertEqual(brute_force_counterexample(hidden, hyp, AB, 3), "")

    def test_alphabet_order_determines_lex_minimum(self):
        # hidden accepts both single-letter words; all-reject hypothesis.
        transitions = {(0, "a"): 1, (0, "b"): 1,
                       (1, "a"): 1, (1, "b"): 1}
        hidden = DFA(2, AB, 0, transitions, {1})
        hyp = DFA(1, AB, 0, {(0, "a"): 0, (0, "b"): 0}, set())
        ce_ab = BoundedEquivalenceOracle(hidden, ("a", "b"), 3).find_counterexample(hyp)
        ce_ba = BoundedEquivalenceOracle(hidden, ("b", "a"), 3).find_counterexample(hyp)
        self.assertEqual(ce_ab, "a")
        self.assertEqual(ce_ba, "b")
        self.assertEqual(brute_force_counterexample(hidden, hyp, ("b", "a"), 3), "b")


class TestLearning(unittest.TestCase):
    def test_learn_ends_with_abb(self):
        hidden = ends_with_dfa("abb", AB)
        learner, oracle, _ = make_learner(hidden, AB, 6)
        checked = BruteForceCheckedOracle(oracle, hidden, AB, 6)
        result = learner.learn(checked)
        self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(hidden, result.hypothesis, AB, 6))
        self.assertEqual(result.hypothesis.num_states, 4)
        self.assertGreater(learner.cache_hits, 0)
        self.assertEqual(learner.new_queries, len(learner.cache))
        for r in result.rounds:
            self.assertEqual(r.new_membership_queries >= 0, True)

    def test_long_suffix_chain(self):
        # Two prefixes ("a"^i, "a"^j) look identical under short suffixes
        # and are only separated by suffixes of length up to n.
        n = 4
        hidden = exact_a_run_dfa(n, AB)
        learner, oracle, _ = make_learner(hidden, AB, 10)
        checked = BruteForceCheckedOracle(oracle, hidden, AB, 10)
        result = learner.learn(checked)
        self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(hidden, result.hypothesis, AB, 10))
        self.assertEqual(result.rounds[0].num_states, 1)
        self.assertEqual(result.hypothesis.num_states, n + 2)
        # the first counterexample is the unique shortest accepted word
        self.assertEqual(result.counterexamples[0], "a" * n)

    def test_empty_string_acceptor(self):
        hidden = exact_a_run_dfa(0, AB)  # accepts only ""
        learner, oracle, _ = make_learner(hidden, AB, 4)
        checked = BruteForceCheckedOracle(oracle, hidden, AB, 4)
        result = learner.learn(checked)
        self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(hidden, result.hypothesis, AB, 4))

    def test_dead_state_machine(self):
        hidden = no_double_letter_dfa("b", AB)
        learner, oracle, _ = make_learner(hidden, AB, 6)
        checked = BruteForceCheckedOracle(oracle, hidden, AB, 6)
        result = learner.learn(checked)
        self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(hidden, result.hypothesis, AB, 6))
        self.assertEqual(result.hypothesis.num_states, 3)

    def test_random_hidden_machines(self):
        rng = random.Random(7)
        for _ in range(10):
            hidden = random_dfa(rng, rng.randint(2, 5), AB)
            learner, oracle, _ = make_learner(hidden, AB, 5)
            checked = BruteForceCheckedOracle(oracle, hidden, AB, 5)
            result = learner.learn(checked)
            self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
            self.assertTrue(domain_equivalent(hidden, result.hypothesis, AB, 5))

    def test_no_undetermined_cells_assumed(self):
        hidden = ends_with_dfa("abb", AB)
        learner, oracle, _ = make_learner(hidden, AB, 6)
        learner.learn(oracle)
        for s in learner.S:
            for e in learner.E:
                self.assertIn(s + e, learner.cache)
            for a in AB:
                for e in learner.E:
                    self.assertIn(s + a + e, learner.cache)

    def test_domain_bounded_claim_only(self):
        # Hidden machine accepts "a"*6, invisible within L=4.
        hidden = exact_a_run_dfa(6, AB)
        learner, oracle, _ = make_learner(hidden, AB, 4)
        result = learner.learn(oracle)
        self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
        for w in iter_words(AB, 4):
            self.assertFalse(result.hypothesis.accepts(w))
        self.assertTrue(hidden.accepts("a" * 6))
        self.assertIn("no claim", result.note)


class TestExternalCounterexamples(unittest.TestCase):
    def setUp(self):
        self.hidden = ends_with_dfa("abb", AB)
        self.learner, self.oracle, _ = make_learner(self.hidden, AB, 6)

    def test_false_counterexample_rejected(self):
        hyp = self.learner.current_hypothesis()
        # "abb" is accepted by the hidden machine; find a word where the
        # current hypothesis already agrees and offer it as a fake ce.
        fake = next(w for w in iter_words(AB, 6)
                    if hyp.accepts(w) == self.hidden.accepts(w))
        snapshot = (list(self.learner.S), list(self.learner.E),
                    dict(self.learner.cache),
                    list(self.learner.pending_suffixes))
        self.assertFalse(self.learner.add_external_counterexample(fake))
        self.assertEqual(snapshot, (self.learner.S, self.learner.E,
                                    self.learner.cache,
                                    self.learner.pending_suffixes))

    def test_true_counterexample_accepted(self):
        hyp = self.learner.current_hypothesis()
        ce = brute_force_counterexample(self.hidden, hyp, AB, 6)
        self.assertIsNotNone(ce)
        self.assertTrue(self.learner.add_external_counterexample(ce))
        result = self.learner.learn(self.oracle)
        self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(self.hidden, result.hypothesis, AB, 6))

    def test_out_of_domain_rejected(self):
        with self.assertRaises(ValueError):
            self.learner.add_external_counterexample("a" * 7)


class TestCheckpointing(unittest.TestCase):
    def setUp(self):
        self.hidden = ends_with_dfa("abb", AB)
        fd, self.path = tempfile.mkstemp(suffix=".json")
        os.close(fd)

    def tearDown(self):
        if os.path.exists(self.path):
            os.unlink(self.path)

    def test_budget_exhaustion_and_resume(self):
        learner, oracle, mq = make_learner(self.hidden, AB, 6, budget=3)
        result = learner.learn(oracle)
        self.assertEqual(result.status, STATUS_BUDGET_UNDECIDED)
        learner.save_checkpoint(self.path)
        resumed = ObservationTableLearner.resume_from_checkpoint(
            self.path, mq, oracle.version, budget=10**6)
        result2 = resumed.learn(oracle)
        self.assertEqual(result2.status, STATUS_EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(self.hidden, result2.hypothesis, AB, 6))

    def test_binding_mismatch_rejected(self):
        learner, oracle, mq = make_learner(self.hidden, AB, 6, budget=2)
        learner.learn(oracle)
        learner.save_checkpoint(self.path)
        with self.assertRaises(CheckpointMismatch):
            ObservationTableLearner.resume_from_checkpoint(
                self.path, mq, oracle.version, max_len=7)
        with self.assertRaises(CheckpointMismatch):
            ObservationTableLearner.resume_from_checkpoint(
                self.path, mq, oracle.version, alphabet=("a", "c"))
        with self.assertRaises(CheckpointMismatch):
            ObservationTableLearner.resume_from_checkpoint(
                self.path, mq, "some-other-version")
        # sanity: matching binding loads fine
        ObservationTableLearner.resume_from_checkpoint(
            self.path, mq, oracle.version, alphabet=AB, max_len=6)

    def test_resume_at_arbitrary_query_points(self):
        # Fresh reference run.
        fresh, oracle_f, _ = make_learner(self.hidden, AB, 6)
        checked_f = BruteForceCheckedOracle(oracle_f, self.hidden, AB, 6)
        fresh_result = fresh.learn(checked_f)
        self.assertEqual(fresh_result.status, STATUS_EQUIVALENT_IN_DOMAIN)

        for first_budget in range(0, 25):
            mq = MembershipOracle(self.hidden)
            oracle = BoundedEquivalenceOracle(self.hidden, AB, 6)
            learner = ObservationTableLearner(AB, 6, mq, oracle.version,
                                              budget=first_budget)
            sessions = 0
            while True:
                sessions += 1
                result = learner.learn(oracle)
                if result.status != STATUS_BUDGET_UNDECIDED:
                    break
                learner.save_checkpoint(self.path)
                learner = ObservationTableLearner.resume_from_checkpoint(
                    self.path, mq, oracle.version, budget=5)
                self.assertLess(sessions, 100)
            self.assertEqual(result.status, STATUS_EQUIVALENT_IN_DOMAIN)
            # final hypothesis behaves identically on the whole domain
            self.assertTrue(domain_equivalent(
                fresh_result.hypothesis, result.hypothesis, AB, 6),
                f"first_budget={first_budget}")
            # exactly the same membership evidence was gathered
            self.assertEqual(fresh.new_queries, learner.new_queries)
            self.assertEqual(fresh.cache, learner.cache)


if __name__ == "__main__":
    unittest.main()
