"""Tests for the bounded equivalence oracle against the brute-force enumerator."""

import random
import unittest

from dfa_learning import (
    BoundedEquivalenceOracle,
    DFA,
    MembershipOracle,
    brute_force_counterexample,
    iter_words,
)

from hidden_machines import (
    ALPHABET,
    epsilon_only_machine,
    reject_all_machine,
    two_word_machine,
)


def random_dfa(rng, num_states, alphabet):
    transitions = {
        (state, symbol): rng.randrange(num_states)
        for state in range(num_states)
        for symbol in alphabet
    }
    accepting = {q for q in range(num_states) if rng.random() < 0.5}
    return DFA(range(num_states), alphabet, 0, transitions, accepting)


class ProductOracleTest(unittest.TestCase):
    def test_matches_brute_force_on_random_machines(self):
        rng = random.Random(20260923)
        for _ in range(100):
            hidden = random_dfa(rng, 4, ALPHABET)
            hypothesis = random_dfa(rng, 3, ALPHABET)
            oracle = BoundedEquivalenceOracle(hidden, ALPHABET, max_len=4)
            fast = oracle.find_counterexample(hypothesis)
            slow = brute_force_counterexample(hidden, hypothesis, ALPHABET, 4)
            self.assertEqual(fast, slow)

    def test_empty_string_difference_is_shortest(self):
        hidden = epsilon_only_machine()
        hypothesis = reject_all_machine()
        oracle = BoundedEquivalenceOracle(hidden, ALPHABET, max_len=3)
        self.assertEqual(oracle.find_counterexample(hypothesis), "")

    def test_alphabet_order_controls_lex_tie_break(self):
        hidden = two_word_machine()
        hypothesis = reject_all_machine()
        forward = BoundedEquivalenceOracle(hidden, ("a", "b"), max_len=4)
        backward = BoundedEquivalenceOracle(hidden, ("b", "a"), max_len=4)
        self.assertEqual(forward.find_counterexample(hypothesis), "ab")
        self.assertEqual(backward.find_counterexample(hypothesis), "ba")

    def test_returns_none_when_domain_equivalent(self):
        hidden = two_word_machine()
        oracle = BoundedEquivalenceOracle(hidden, ALPHABET, max_len=6)
        self.assertIsNone(oracle.find_counterexample(hidden))

    def test_domain_bound_is_respected(self):
        # Discrepancy only appears at length 5 ("aaaaa" accepted by hidden).
        from hidden_machines import long_suffix_machine

        hidden = long_suffix_machine()
        hypothesis = reject_all_machine()
        short = BoundedEquivalenceOracle(hidden, ALPHABET, max_len=4)
        long = BoundedEquivalenceOracle(hidden, ALPHABET, max_len=5)
        self.assertIsNone(short.find_counterexample(hypothesis))
        self.assertEqual(long.find_counterexample(hypothesis), "aaaaa")

    def test_membership_oracle_counts_queries(self):
        oracle = MembershipOracle(two_word_machine(), max_len=4)
        self.assertTrue(oracle.query("ab"))
        self.assertFalse(oracle.query("aa"))
        self.assertEqual(oracle.query_count, 2)


if __name__ == "__main__":
    unittest.main()
