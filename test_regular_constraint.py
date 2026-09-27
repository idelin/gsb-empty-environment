"""Unit tests for the DFA regular constraint (Python 3.11, stdlib only).

Run with:  python3.11 -m unittest -v
"""

from __future__ import annotations

import random
import unittest

from automaton import DFA
from independent_checker import check_witness, exhaustive_support
from regular_constraint import RegularConstraint


def even_ones_dfa() -> DFA:
    transitions = {
        ("even", 0): "even",
        ("even", 1): "odd",
        ("odd", 0): "odd",
        ("odd", 1): "even",
    }
    return DFA({"even", "odd"}, {0, 1}, transitions, "even", {"even"})


def ends_with_a_dfa() -> DFA:
    transitions = {
        ("expect", "a"): "got_a",
        ("expect", "b"): "expect",
        ("got_a", "a"): "got_a",
        ("got_a", "b"): "expect",
    }
    return DFA({"expect", "got_a"}, {"a", "b"}, transitions, "expect", {"got_a"})


def random_dfa(rng: random.Random, num_states: int, alphabet) -> DFA:
    states = list(range(num_states))
    transitions = {
        (q, a): rng.choice(states) for q in states for a in alphabet
    }
    accepting = {q for q in states if rng.random() < 0.5}
    return DFA(states, set(alphabet), transitions, rng.choice(states), accepting)


class TestBasicFiltering(unittest.TestCase):
    def test_even_ones_full_domains(self):
        c = RegularConstraint(even_ones_dfa(), [{0, 1}] * 3)
        self.assertTrue(c.filter())
        # Every position keeps both values: 0 and 1 each appear in some
        # even-weight word of length 3.
        for i in range(3):
            self.assertEqual(c.domains[i], {0, 1})

    def test_even_ones_pinned_first(self):
        c = RegularConstraint(even_ones_dfa(), [{1}, {0, 1}, {0, 1}])
        self.assertTrue(c.filter())
        # X0 = 1 forces an odd number of 1s in the suffix; both values
        # remain possible at each suffix position.
        self.assertEqual(c.domains[0], {1})
        self.assertEqual(c.domains[1], {0, 1})
        self.assertEqual(c.domains[2], {0, 1})

    def test_dead_path_value_removed(self):
        # "ends with a": with n = 1 the only live value is 'a'; 'b' leads
        # exclusively to a dead (non-accepting) end and must be pruned.
        c = RegularConstraint(ends_with_a_dfa(), [{"a", "b"}])
        self.assertTrue(c.filter())
        self.assertEqual(c.domains[0], {"a"})

    def test_last_position_forced(self):
        c = RegularConstraint(ends_with_a_dfa(), [{"a", "b"}] * 3)
        self.assertTrue(c.filter())
        self.assertEqual(c.domains[2], {"a"})
        self.assertEqual(c.domains[0], {"a", "b"})
        self.assertEqual(c.domains[1], {"a", "b"})

    def test_unsatisfiable_when_domain_emptied(self):
        c = RegularConstraint(ends_with_a_dfa(), [{"a", "b"}, {"b"}])
        self.assertFalse(c.filter())

    def test_witness_passes_independent_check(self):
        c = RegularConstraint(even_ones_dfa(), [{0, 1}] * 5)
        self.assertTrue(c.filter())
        witness = c.witness()
        self.assertIsNotNone(witness)
        self.assertTrue(check_witness(c.dfa, c.original_domains, witness))

    def test_checker_rejects_bad_witness(self):
        dfa = even_ones_dfa()
        domains = [{0, 1}] * 2
        self.assertFalse(check_witness(dfa, domains, [0, 1]))  # odd weight
        self.assertFalse(check_witness(dfa, domains, [0]))      # wrong length
        self.assertFalse(check_witness(dfa, domains, [0, 7]))   # outside domain
        self.assertFalse(check_witness(dfa, domains, None))


class TestEmptySequence(unittest.TestCase):
    def test_empty_accepted_when_start_accepting(self):
        c = RegularConstraint(even_ones_dfa(), [])
        self.assertTrue(c.is_satisfiable())
        self.assertEqual(c.witness(), [])
        self.assertTrue(check_witness(c.dfa, [], []))

    def test_empty_rejected_when_start_not_accepting(self):
        c = RegularConstraint(ends_with_a_dfa(), [])
        self.assertFalse(c.is_satisfiable())
        self.assertIsNone(c.witness())

    def test_empty_matches_exhaustive(self):
        for dfa in (even_ones_dfa(), ends_with_a_dfa()):
            sat, support = exhaustive_support(dfa, [])
            self.assertEqual(sat, dfa.accepts([]))
            self.assertEqual(support, [])


class TestIncrementalUpdates(unittest.TestCase):
    def test_remove_value_makes_unsat(self):
        c = RegularConstraint(ends_with_a_dfa(), [{"a", "b"}] * 2)
        self.assertTrue(c.is_satisfiable())
        self.assertFalse(c.remove_value(1, "a"))  # last symbol must be 'a'

    def test_remove_absent_value_is_noop(self):
        c = RegularConstraint(ends_with_a_dfa(), [{"a"}, {"a", "b"}])
        self.assertTrue(c.remove_value(0, "b"))

    def test_incremental_matches_fresh_recompute(self):
        rng = random.Random(20260927)
        alphabet = [0, 1, 2]
        for _ in range(50):
            dfa = random_dfa(rng, rng.randint(1, 4), alphabet)
            n = rng.randint(1, 5)
            domains = [
                {a for a in alphabet if rng.random() < 0.8} for _ in range(n)
            ]
            incremental = RegularConstraint(dfa, domains)
            # Apply a random sequence of removals / assignments.
            for _ in range(rng.randint(1, 6)):
                i = rng.randrange(n)
                if rng.random() < 0.5 and incremental.domains[i]:
                    incremental.remove_value(i, rng.choice(alphabet))
                else:
                    incremental.set_domain(
                        i, {a for a in alphabet if rng.random() < 0.6}
                    )
            fresh = RegularConstraint(dfa, incremental.domains)
            self.assertEqual(incremental.is_satisfiable(), fresh.is_satisfiable())
            for i in range(n):
                self.assertEqual(
                    incremental.supported_values(i), fresh.supported_values(i)
                )


class TestAgainstExhaustiveEnumeration(unittest.TestCase):
    def test_fixed_dfas(self):
        cases = [
            (even_ones_dfa(), [{0, 1}] * 4),
            (even_ones_dfa(), [{1}, {0, 1}, {0}]),
            (ends_with_a_dfa(), [{"a", "b"}] * 3),
            (ends_with_a_dfa(), [{"b"}, {"a", "b"}]),
            (even_ones_dfa(), []),
            (ends_with_a_dfa(), []),
        ]
        for dfa, domains in cases:
            with self.subTest(domains=domains):
                c = RegularConstraint(dfa, domains)
                c.filter()
                truth_sat, truth = exhaustive_support(dfa, domains)
                self.assertEqual(c.is_satisfiable(), truth_sat)
                self.assertEqual([set(d) for d in c.domains], truth)
                if truth_sat:
                    self.assertTrue(
                        check_witness(dfa, c.original_domains, c.witness())
                    )

    def test_random_dfas_match_exhaustive(self):
        rng = random.Random(1234567)
        alphabet = [0, 1, 2]
        for trial in range(200):
            dfa = random_dfa(rng, rng.randint(1, 4), alphabet)
            n = rng.randint(0, 5)
            domains = [
                {a for a in alphabet if rng.random() < 0.75} for _ in range(n)
            ]
            c = RegularConstraint(dfa, domains)
            c.filter()
            truth_sat, truth = exhaustive_support(dfa, domains)
            with self.subTest(trial=trial, n=n, domains=domains):
                self.assertEqual(c.is_satisfiable(), truth_sat)
                self.assertEqual([set(d) for d in c.domains], truth)
                if truth_sat:
                    self.assertTrue(
                        check_witness(dfa, c.original_domains, c.witness())
                    )
                else:
                    self.assertIsNone(c.witness())


if __name__ == "__main__":
    unittest.main()
