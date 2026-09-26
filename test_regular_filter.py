"""Unit tests for regular_filter (DFA sequence domain filtering)."""

import random
import unittest

from regular_filter import (
    DFA,
    RegularFilter,
    brute_force_supported_values,
    check_solution,
)


def make_even_ones_dfa():
    """Accepts binary words with an even number of 1s. States: 0=even, 1=odd."""
    transitions = {
        (0, 0): 0, (0, 1): 1,
        (1, 0): 1, (1, 1): 0,
    }
    return DFA(num_states=2, start=0, accepts={0}, transitions=transitions)


def make_dead_path_dfa():
    """Value 2 from the start state leads only to a dead (non-accepting)
    sink, so it can never appear in an accepted word."""
    # states: 0=start, 1=alive, 2=dead sink
    transitions = {
        (0, 0): 1, (0, 1): 1, (0, 2): 2,
        (1, 0): 1, (1, 1): 1, (1, 2): 2,
        (2, 0): 2, (2, 1): 2, (2, 2): 2,
    }
    return DFA(num_states=3, start=0, accepts={1}, transitions=transitions)


def random_dfa(rng, num_states, alphabet):
    transitions = {}
    for s in range(num_states):
        for a in alphabet:
            transitions[(s, a)] = rng.randrange(num_states)
    accepts = {s for s in range(num_states) if rng.random() < 0.5}
    return DFA(num_states=num_states, start=0, accepts=accepts,
               transitions=transitions)


class TestFilteringAgainstBruteForce(unittest.TestCase):
    """Filtered domains must match exhaustive enumeration exactly."""

    def test_random_instances_match_brute_force(self):
        rng = random.Random(20260927)
        for trial in range(300):
            alphabet = tuple(range(rng.randint(1, 3)))
            dfa = random_dfa(rng, rng.randint(1, 4), alphabet)
            n = rng.randint(1, 5)
            domains = [
                {a for a in alphabet if rng.random() < 0.8} or {alphabet[0]}
                for _ in range(n)
            ]
            filt = RegularFilter(dfa, domains)
            expected, any_accepted = brute_force_supported_values(dfa, domains)
            self.assertEqual(
                [set(d) for d in expected], filt.domains,
                msg=f"trial {trial}: domains mismatch",
            )
            self.assertEqual(any_accepted, filt.is_satisfiable(),
                             msg=f"trial {trial}: satisfiability mismatch")
            word = filt.solve()
            if any_accepted:
                self.assertIsNotNone(word)
                self.assertTrue(
                    check_solution(dfa, [frozenset(d) for d in domains], word))
            else:
                self.assertIsNone(word)

    def test_dead_path_value_removed(self):
        dfa = make_dead_path_dfa()
        filt = RegularFilter(dfa, [{0, 1, 2}, {0, 1, 2}])
        # 2 only reaches the dead sink: removed everywhere.
        self.assertEqual(filt.domains, [{0, 1}, {0, 1}])
        self.assertTrue(filt.is_satisfiable())

    def test_unsatisfiable_when_no_accept_reachable(self):
        # Only state is non-accepting: no non-empty word is accepted.
        dfa = DFA(1, 0, set(), {(0, 0): 0, (0, 1): 0})
        filt = RegularFilter(dfa, [{0, 1}, {0, 1}])
        self.assertFalse(filt.is_satisfiable())
        self.assertEqual(filt.domains, [set(), set()])
        self.assertIsNone(filt.solve())

    def test_even_ones_example(self):
        dfa = make_even_ones_dfa()
        domains = [{0, 1}] * 4
        filt = RegularFilter(dfa, domains)
        # All values remain supported (both parities extendable to even).
        self.assertEqual(filt.domains, [{0, 1}] * 4)
        word = filt.solve()
        self.assertTrue(check_solution(dfa, [frozenset({0, 1})] * 4, word))
        self.assertEqual(word.count(1) % 2, 0)


class TestEmptySequence(unittest.TestCase):
    def test_empty_sequence_accepted_when_start_accepting(self):
        dfa = make_even_ones_dfa()  # start state 0 is accepting
        filt = RegularFilter(dfa, [])
        self.assertTrue(filt.is_satisfiable())
        self.assertEqual(filt.solve(), ())
        self.assertTrue(check_solution(dfa, [], ()))

    def test_empty_sequence_rejected_when_start_not_accepting(self):
        dfa = DFA(2, 0, {1}, {(0, 0): 0, (0, 1): 1, (1, 0): 1, (1, 1): 1})
        filt = RegularFilter(dfa, [])
        self.assertFalse(filt.is_satisfiable())
        self.assertIsNone(filt.solve())
        self.assertFalse(check_solution(dfa, [], ()))

    def test_empty_sequence_matches_brute_force(self):
        rng = random.Random(7)
        for _ in range(50):
            dfa = random_dfa(rng, rng.randint(1, 3), (0, 1))
            filt = RegularFilter(dfa, [])
            _, any_accepted = brute_force_supported_values(dfa, [])
            self.assertEqual(any_accepted, filt.is_satisfiable())
            self.assertEqual(any_accepted, dfa.start in dfa.accepts)


class TestIncrementalUpdates(unittest.TestCase):
    def test_remove_value_matches_fresh_filter(self):
        rng = random.Random(99)
        for trial in range(200):
            alphabet = (0, 1, 2)
            dfa = random_dfa(rng, rng.randint(1, 4), alphabet)
            n = rng.randint(1, 5)
            domains = [set(alphabet) for _ in range(n)]
            filt = RegularFilter(dfa, domains)
            # Apply a random sequence of incremental removals.
            for _ in range(rng.randint(1, 4)):
                i = rng.randrange(n)
                if len(filt.domains[i]) > 1:
                    filt.remove_value(i, rng.choice(sorted(filt.domains[i])))
            # Filtering is a fixpoint, so a fresh full propagation of the
            # current domains must reproduce the incremental result exactly.
            fresh = RegularFilter(dfa, filt.domains)
            self.assertEqual(filt.domains, fresh.domains,
                             msg=f"trial {trial}")
            self.assertEqual(filt.is_satisfiable(), fresh.is_satisfiable())
            # And it must still agree with exhaustive enumeration of the
            # current domains.
            expected, any_accepted = brute_force_supported_values(
                dfa, filt.domains)
            self.assertEqual([set(d) for d in expected], filt.domains)
            self.assertEqual(any_accepted, filt.is_satisfiable())

    def test_assign_propagates_to_other_positions(self):
        dfa = make_even_ones_dfa()
        filt = RegularFilter(dfa, [{0, 1}] * 3)
        filt.assign(0, 1)
        filt.assign(1, 1)
        # Two 1s already: position 2 must be 0 to keep the count even.
        self.assertEqual(filt.domains[2], {0})
        self.assertTrue(filt.is_satisfiable())
        self.assertEqual(filt.solve(), (1, 1, 0))

    def test_assign_to_unsupported_value_raises(self):
        dfa = make_dead_path_dfa()
        filt = RegularFilter(dfa, [{0, 1, 2}])
        with self.assertRaises(ValueError):
            filt.assign(0, 2)  # 2 was filtered out (dead path only)

    def test_incremental_removal_can_detect_unsat(self):
        # Accepts exactly the word (1, 1): start -1-> mid -1-> accept.
        transitions = {
            (0, 0): 3, (0, 1): 1,
            (1, 0): 3, (1, 1): 2,
            (2, 0): 3, (2, 1): 3,
            (3, 0): 3, (3, 1): 3,
        }
        dfa = DFA(4, 0, {2}, transitions)
        filt = RegularFilter(dfa, [{0, 1}, {0, 1}])
        self.assertEqual(filt.domains, [{1}, {1}])  # only (1,1) survives
        filt.remove_value(0, 1)
        self.assertFalse(filt.is_satisfiable())
        self.assertIsNone(filt.solve())


class TestIndependentChecker(unittest.TestCase):
    def test_checker_accepts_valid_word(self):
        dfa = make_even_ones_dfa()
        domains = [frozenset({0, 1})] * 4
        self.assertTrue(check_solution(dfa, domains, (1, 1, 0, 0)))

    def test_checker_rejects_word_outside_original_domain(self):
        dfa = make_even_ones_dfa()
        domains = [frozenset({0}), frozenset({0, 1})]
        # (0, 0) is accepted by the DFA and in-domain; tamper the domain.
        self.assertTrue(check_solution(dfa, domains, (0, 0)))
        self.assertFalse(check_solution(dfa, domains, (1, 0)))

    def test_checker_rejects_non_accepted_word(self):
        dfa = make_even_ones_dfa()
        domains = [frozenset({0, 1})] * 3
        self.assertFalse(check_solution(dfa, domains, (1, 0, 0)))  # odd count

    def test_checker_rejects_wrong_length(self):
        dfa = make_even_ones_dfa()
        domains = [frozenset({0, 1})] * 2
        self.assertFalse(check_solution(dfa, domains, (0,)))
        self.assertFalse(check_solution(dfa, domains, (0, 0, 0)))


class TestDfaValidation(unittest.TestCase):
    def test_missing_transition_rejected(self):
        with self.assertRaises(ValueError):
            DFA(2, 0, {1}, {(0, 0): 1})  # not total

    def test_domain_outside_alphabet_rejected(self):
        dfa = make_even_ones_dfa()
        with self.assertRaises(ValueError):
            RegularFilter(dfa, [{0, 5}])


if __name__ == "__main__":
    unittest.main()
