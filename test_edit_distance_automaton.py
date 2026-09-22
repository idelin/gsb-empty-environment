"""unittest suite for edit_distance_automaton. Run: python3.11 -m unittest -v"""

import random
import unittest
from functools import lru_cache

from edit_distance_automaton import EditCosts, EditDistanceAutomaton

ABC = "abc"


def reference_distance(pattern, text, costs):
    """Independent naive recursive weighted edit distance (no transpose)."""

    @lru_cache(maxsize=None)
    def go(i, j):
        if i == len(pattern):
            return (len(text) - j) * costs.insert
        if j == len(text):
            return (len(pattern) - i) * costs.delete
        best = go(i + 1, j) + costs.delete
        best = min(best, go(i, j + 1) + costs.insert)
        sub = 0 if pattern[i] == text[j] else costs.substitute
        best = min(best, go(i + 1, j + 1) + sub)
        return best

    return go(0, 0)


def apply_ops(pattern, ops):
    """Replay an edit path on the pattern; returns the produced string."""
    out = []
    i = 0
    for op in ops:
        if op.kind == "match":
            assert pattern[i] == op.pattern_chars
            out.append(op.text_chars)
            i += 1
        elif op.kind == "substitute":
            assert pattern[i] == op.pattern_chars
            out.append(op.text_chars)
            i += 1
        elif op.kind == "delete":
            assert pattern[i] == op.pattern_chars
            i += 1
        elif op.kind == "insert":
            out.append(op.text_chars)
        elif op.kind == "transpose":
            assert pattern[i:i + 2] == op.pattern_chars
            assert op.pattern_chars[::-1] == op.text_chars
            out.append(op.text_chars)
            i += 2
        else:  # pragma: no cover
            raise AssertionError(f"unknown op {op.kind}")
    assert i == len(pattern), "path does not consume the whole pattern"
    return "".join(out)


class TestEmptyPattern(unittest.TestCase):
    def test_empty_pattern_matches_strings_up_to_length_k(self):
        aut = EditDistanceAutomaton("", ABC, k=2)
        self.assertTrue(aut.match("")[0])
        self.assertTrue(aut.match("a")[0])
        self.assertTrue(aut.match("ab")[0])
        self.assertFalse(aut.match("abc")[0])

    def test_empty_pattern_cost_is_insertions(self):
        aut = EditDistanceAutomaton("", ABC, k=5)
        self.assertEqual(aut.match("ab")[1], 2)
        dist, ops = aut.min_distance_and_path("ab")
        self.assertEqual(dist, 2)
        self.assertTrue(all(op.kind == "insert" for op in ops))

    def test_empty_text_matches_via_deletions(self):
        aut = EditDistanceAutomaton("ab", ABC, k=2)
        self.assertTrue(aut.match("")[0])
        self.assertEqual(aut.best_cost(), 2)
        aut2 = EditDistanceAutomaton("abc", ABC, k=2)
        self.assertFalse(aut2.match("")[0])


class TestBasicDistance(unittest.TestCase):
    def test_kitten_sitting(self):
        aut = EditDistanceAutomaton("kitten", "aeikgnst", k=3)
        accepted, dist, ops = aut.explain("sitting")
        self.assertTrue(accepted)
        self.assertEqual(dist, 3)
        self.assertEqual(apply_ops("kitten", ops), "sitting")
        self.assertEqual(sum(op.cost for op in ops), dist)

    def test_identity_is_zero(self):
        aut = EditDistanceAutomaton("abc", "abcd", k=0)
        self.assertTrue(aut.match("abc")[0])
        self.assertEqual(aut.best_cost(), 0)
        self.assertFalse(aut.match("abd")[0])

    def test_k_cutoff_prunes(self):
        aut = EditDistanceAutomaton("aaaa", ABC, k=1)
        self.assertFalse(aut.match("bbbb")[0])
        self.assertIsNone(aut.best_cost())  # accepting state pruned
        self.assertEqual(aut.min_distance("bbbb"), 4)  # batch still exact

    def test_weighted_costs_prefer_delete_insert_over_substitute(self):
        costs = EditCosts(insert=1, delete=1, substitute=5)
        aut = EditDistanceAutomaton("ab", "abcd", k=10, costs=costs)
        dist, ops = aut.min_distance_and_path("cd")
        self.assertEqual(dist, 4)  # 2 deletes + 2 inserts beats 2 substitutes (10)
        self.assertEqual(apply_ops("ab", ops), "cd")
        kinds = [op.kind for op in ops]
        self.assertNotIn("substitute", kinds)


class TestStreaming(unittest.TestCase):
    def test_state_set_keeps_best_cost_per_state(self):
        # substitute (cost 1) and delete+insert (cost 3+1) both reach state 1;
        # only the optimal cost may survive.
        costs = EditCosts(insert=1, delete=3, substitute=1)
        aut = EditDistanceAutomaton("a", ABC, k=9, costs=costs)
        aut.step("b")
        self.assertEqual(aut.states, {0: 1, 1: 1})

    def test_streaming_state_set_updates_step_by_step(self):
        aut = EditDistanceAutomaton("ab", ABC, k=2)
        aut.step("a")
        self.assertEqual(aut.states, {0: 1, 1: 0, 2: 1})
        aut.step("b")
        self.assertEqual(aut.states[2], 0)
        self.assertTrue(aut.accepts())

    def test_streaming_agrees_with_batch_dp(self):
        rng = random.Random(20260922)
        cases = [
            (EditCosts(), None),
            (EditCosts(insert=2, delete=1, substitute=3), None),
            (EditCosts(insert=1, delete=1, substitute=1, transpose=1), None),
            (EditCosts(insert=2, delete=2, substitute=1, transpose=3), None),
        ]
        for costs, _ in cases:
            for _ in range(60):
                m, n = rng.randint(0, 5), rng.randint(0, 5)
                pat = "".join(rng.choice(ABC) for _ in range(m))
                txt = "".join(rng.choice(ABC) for _ in range(n))
                k = rng.randint(0, 8)
                aut = EditDistanceAutomaton(pat, ABC, k=k, costs=costs)
                accepted, best = aut.match(txt)
                dist = aut.min_distance(txt)
                self.assertEqual(accepted, dist <= k, (pat, txt, k, costs))
                if dist <= k:
                    self.assertEqual(best, dist, (pat, txt, k, costs))
                else:
                    self.assertIsNone(best, (pat, txt, k, costs))

    def test_streaming_reset_reuse(self):
        aut = EditDistanceAutomaton("ab", ABC, k=1)
        self.assertTrue(aut.match("ab")[0])
        self.assertFalse(aut.match("zz" if "z" in ABC else "cc")[0])
        self.assertTrue(aut.match("ab")[0])  # state fully reset


class TestTransposition(unittest.TestCase):
    def test_swap_is_not_a_single_edit_by_default(self):
        aut = EditDistanceAutomaton("ab", ABC, k=2)
        self.assertEqual(aut.min_distance("ba"), 2)
        self.assertFalse(EditDistanceAutomaton("ab", ABC, k=1).match("ba")[0])

    def test_swap_is_single_edit_when_declared(self):
        costs = EditCosts(transpose=1)
        aut = EditDistanceAutomaton("ab", ABC, k=1, costs=costs)
        accepted, dist, ops = aut.explain("ba")
        self.assertTrue(accepted)
        self.assertEqual(dist, 1)
        self.assertEqual([op.kind for op in ops], ["transpose"])
        self.assertEqual(apply_ops("ab", ops), "ba")

    def test_transpose_streaming_matches_batch(self):
        costs = EditCosts(transpose=1)
        aut = EditDistanceAutomaton("abcd", "abcd", k=2, costs=costs)
        self.assertEqual(aut.match("acbd"), (True, 1))
        self.assertEqual(aut.min_distance("acbd"), 1)
        self.assertEqual(aut.match("adcb"), (True, 2))  # two disjoint swaps

    def test_transpose_weighted(self):
        # transpose cost 3 loses to delete+insert (1+1=2)
        costs = EditCosts(insert=1, delete=1, substitute=5, transpose=3)
        aut = EditDistanceAutomaton("ab", ABC, k=5, costs=costs)
        self.assertEqual(aut.min_distance("ba"), 2)


class TestAlphabetAndTransitions(unittest.TestCase):
    def test_pattern_symbol_must_be_in_alphabet(self):
        with self.assertRaises(ValueError):
            EditDistanceAutomaton("az", ABC, k=1)

    def test_input_symbol_must_be_in_alphabet(self):
        aut = EditDistanceAutomaton("a", ABC, k=1)
        with self.assertRaises(ValueError):
            aut.step("z")
        with self.assertRaises(ValueError):
            aut.min_distance("z")

    def test_explicit_transition_structure(self):
        costs = EditCosts(insert=2, delete=3, substitute=4)
        aut = EditDistanceAutomaton("ab", ABC, k=9, costs=costs)
        self.assertEqual(
            aut.transitions(0, "a"),
            [(0, 2, "insert"), (1, 0, "match")],
        )
        self.assertEqual(
            aut.transitions(0, "b"),
            [(0, 2, "insert"), (1, 4, "substitute")],
        )
        self.assertEqual(aut.transitions(2, "a"), [(2, 2, "insert")])
        self.assertEqual(aut.epsilon_transitions(0), [(1, 3, "delete")])
        self.assertEqual(aut.epsilon_transitions(2), [])


class TestAgainstIndependentReference(unittest.TestCase):
    def test_matches_naive_recursive_reference(self):
        rng = random.Random(7)
        for costs in (EditCosts(), EditCosts(insert=2, delete=3, substitute=1)):
            for _ in range(80):
                pat = "".join(rng.choice(ABC) for _ in range(rng.randint(0, 5)))
                txt = "".join(rng.choice(ABC) for _ in range(rng.randint(0, 5)))
                aut = EditDistanceAutomaton(pat, ABC, k=99, costs=costs)
                dist, ops = aut.min_distance_and_path(txt)
                self.assertEqual(dist, reference_distance(pat, txt, costs),
                                 (pat, txt, costs))
                self.assertEqual(apply_ops(pat, ops), txt)
                self.assertEqual(sum(op.cost for op in ops), dist)


if __name__ == "__main__":
    unittest.main()
