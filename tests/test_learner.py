"""Tests for the observation-table learner."""

import unittest

from dfa_learning import (
    BUDGET_EXHAUSTED,
    EQUIVALENT_IN_DOMAIN,
    HYPOTHESIS_READY,
    BoundedEquivalenceOracle,
    Learner,
    MembershipOracle,
    brute_force_counterexample,
    domain_equivalent,
)

from hidden_machines import (
    ALPHABET,
    dead_state_machine,
    empty_string_machine,
    long_suffix_machine,
)

VERSION = "hidden-v1"


def make_rig(hidden, max_len, budget=10 ** 9):
    membership = MembershipOracle(hidden, max_len, version=VERSION)
    equivalence = BoundedEquivalenceOracle(hidden, ALPHABET, max_len, version=VERSION)
    learner = Learner(membership, ALPHABET, max_len, budget)
    return learner, equivalence


class LearnMachinesTest(unittest.TestCase):
    def test_prefixes_distinguished_only_by_long_suffix(self):
        hidden = long_suffix_machine()
        learner, equivalence = make_rig(hidden, max_len=12)
        result = learner.learn(equivalence)
        self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
        hypothesis = result.hypothesis
        # Independent full enumeration confirms equivalence inside the domain.
        self.assertTrue(domain_equivalent(hidden, hypothesis, ALPHABET, 12))
        # The two prefixes "a"/"b" can only be told apart by a suffix of
        # length >= 4, so the table must have acquired such a column.
        self.assertGreaterEqual(max(len(e) for e in learner.E), 4)
        # Sanity: the hypothesis really separates the two prefixes.
        self.assertNotEqual(
            hypothesis.run("a"),
            hypothesis.run("b"),
        )

    def test_empty_string_acceptance_is_learned(self):
        hidden = empty_string_machine()
        learner, equivalence = make_rig(hidden, max_len=5)
        result = learner.learn(equivalence)
        self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
        self.assertTrue(result.hypothesis.accepts(""))
        self.assertTrue(domain_equivalent(hidden, result.hypothesis, ALPHABET, 5))

    def test_dead_state_machine(self):
        hidden = dead_state_machine()
        learner, equivalence = make_rig(hidden, max_len=6)
        result = learner.learn(equivalence)
        self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
        hypothesis = result.hypothesis
        self.assertTrue(domain_equivalent(hidden, hypothesis, ALPHABET, 6))
        # A non-accepting sink survives in the learned hypothesis.
        sinks = [
            q for q in hypothesis.states
            if not hypothesis.is_accepting(q)
            and all(hypothesis.step(q, a) == q for a in ALPHABET)
        ]
        self.assertTrue(sinks)
        self.assertFalse(hypothesis.accepts("bb"))

    def test_round_statistics_add_up(self):
        hidden = dead_state_machine()
        learner, equivalence = make_rig(hidden, max_len=6)
        result = learner.learn(equivalence)
        self.assertGreaterEqual(len(result.rounds), 1)
        self.assertEqual(
            sum(r["new_membership_queries"] for r in result.rounds),
            result.membership_queries,
        )
        self.assertEqual(
            sum(r["cache_hits"] for r in result.rounds),
            result.cache_hits,
        )
        self.assertEqual(result.equivalence_queries, len(result.rounds))
        for record in result.rounds:
            self.assertGreaterEqual(record["states"], 1)


class BudgetTest(unittest.TestCase):
    def test_zero_budget_leaves_cells_unknown_not_rejected(self):
        hidden = dead_state_machine()
        learner, equivalence = make_rig(hidden, max_len=6, budget=0)
        result = learner.learn(equivalence)
        self.assertEqual(result.status, BUDGET_EXHAUSTED)
        self.assertIsNone(result.hypothesis)
        # Undetermined cells stay None; nothing is fabricated as rejection.
        self.assertIn(None, learner.T.values())
        self.assertNotIn(False, learner.T.values())
        self.assertNotIn(True, learner.T.values())

    def test_partial_budget_keeps_only_verified_evidence(self):
        hidden = dead_state_machine()
        learner, equivalence = make_rig(hidden, max_len=6, budget=3)
        result = learner.learn(equivalence)
        self.assertEqual(result.status, BUDGET_EXHAUSTED)
        for (s, e), value in learner.T.items():
            if value is not None:
                self.assertEqual(value, hidden.accepts(s + e))

    def test_resume_after_budget_stop_converges(self):
        hidden = dead_state_machine()
        learner, equivalence = make_rig(hidden, max_len=6, budget=2)
        self.assertEqual(learner.learn(equivalence).status, BUDGET_EXHAUSTED)
        learner.budget = 10 ** 9
        result = learner.learn(equivalence)
        self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
        self.assertTrue(domain_equivalent(hidden, result.hypothesis, ALPHABET, 6))


class ExternalCounterexampleTest(unittest.TestCase):
    def setUp(self):
        self.hidden = dead_state_machine()
        self.max_len = 6
        self.learner, self.equivalence = make_rig(self.hidden, self.max_len)
        ready = self.learner.learn()  # no equivalence oracle: hypothesis only
        self.assertEqual(ready.status, HYPOTHESIS_READY)
        self.hypothesis = ready.hypothesis

    def snapshot(self):
        return (list(self.learner.S), list(self.learner.E), dict(self.learner.T))

    def test_wrong_counterexample_is_rejected_without_pollution(self):
        # Find a word where the hypothesis already agrees with the hidden DFA.
        wrong = next(
            w for w in ("", "a", "b", "ab", "ba")
            if self.hypothesis.accepts(w) == self.hidden.accepts(w)
        )
        before = self.snapshot()
        self.assertFalse(self.learner.submit_counterexample(wrong))
        self.assertEqual(self.snapshot(), before)

    def test_out_of_domain_counterexample_is_rejected(self):
        before = self.snapshot()
        self.assertFalse(self.learner.submit_counterexample("a" * (self.max_len + 1)))
        self.assertEqual(self.snapshot(), before)

    def test_valid_counterexample_is_verified_and_used(self):
        # The first hypothesis of this machine is wrong on "bb".
        self.assertFalse(self.hidden.accepts("bb"))
        self.assertTrue(self.hypothesis.accepts("bb"))
        self.assertTrue(self.learner.submit_counterexample("bb"))
        self.assertIn("bb", self.learner.E)
        # Learning can now continue to domain equivalence.
        result = self.learner.learn(self.equivalence)
        self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
        self.assertTrue(
            domain_equivalent(self.hidden, result.hypothesis, ALPHABET, self.max_len)
        )

    def test_counterexample_is_checked_against_oracle_not_hypothesis(self):
        # "b" is misclassified neither by hidden nor hypothesis, so even
        # though it is a short word it must be rejected as evidence.
        before = self.snapshot()
        self.assertFalse(self.learner.submit_counterexample("b"))
        self.assertEqual(self.snapshot(), before)


if __name__ == "__main__":
    unittest.main()
