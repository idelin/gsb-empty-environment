"""Unit tests for shrinker.py (synthetic data only)."""

import unittest

from shrinker import (
    PREDICATES,
    cost,
    neighbors,
    pred_consecutive_equal_nonzero,
    shrink,
)


def reachable_states(vector, cap=100_000):
    """Brute-force enumeration of every state reachable from ``vector``."""
    seen = {tuple(vector)}
    stack = [tuple(vector)]
    while stack:
        s = stack.pop()
        for n in neighbors(s):
            if n not in seen:
                seen.add(n)
                assert len(seen) <= cap, "state space exploded in test"
                stack.append(n)
    return seen


def brute_force_minimum(vector, predicate):
    """Independent oracle: cheapest reachable failing state, or None."""
    failing = [s for s in reachable_states(vector) if predicate(list(s))]
    return min(failing, key=cost) if failing else None


class TestAcceptanceBoundary(unittest.TestCase):
    def test_449_shrinks_to_11_not_minus_11(self):
        # Sign changes are not a legal move, so [-1, -1] is unreachable
        # from [4, 4, 9]; the documented answer is [1, 1].
        report = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=10_000)
        self.assertEqual(report["result"], [1, 1])
        self.assertTrue(report["proven_minimal"])
        self.assertFalse(report["budget_exhausted"])

    def test_negative_input_keeps_negative_sign(self):
        report = shrink([-4, -4, 9], pred_consecutive_equal_nonzero, budget=10_000)
        self.assertEqual(report["result"], [-1, -1])
        self.assertTrue(report["proven_minimal"])

    def test_moves_never_change_sign(self):
        for state in reachable_states([3, -2, 0]):
            for nxt in neighbors(state):
                if len(nxt) == len(state):  # reduction move, not deletion
                    for a, b in zip(state, nxt):
                        self.assertGreaterEqual(a * b, 0)  # same sign or zero


class TestGlobalMinimality(unittest.TestCase):
    def test_matches_brute_force_oracle(self):
        cases = [
            ([4, 4, 9], "consecutive_equal_nonzero"),
            ([-3, -3, 2, 2], "consecutive_equal_nonzero"),
            ([5, 5], "any_nonzero"),
            ([-2, 7], "any_nonzero"),
            ([3, -3, 8], "zero_sum_pair"),
            ([6, 6, 1], "sum_at_least_ten"),
            ([0, 0, 0], "any_nonzero"),
            ([1, 2, 3], "consecutive_equal_nonzero"),
        ]
        for vector, name in cases:
            with self.subTest(vector=vector, predicate=name):
                pred = PREDICATES[name]
                report = shrink(vector, pred, budget=100_000)
                oracle = brute_force_minimum(vector, pred)
                expected = list(oracle) if oracle is not None else None
                self.assertEqual(report["result"], expected)
                self.assertTrue(report["proven_minimal"])

    def test_deletion_beats_reduction_when_shorter(self):
        # [5, 5] fails any_nonzero; length-1 [1] beats [1, 1].
        report = shrink([5, 5], PREDICATES["any_nonzero"], budget=10_000)
        self.assertEqual(report["result"], [1])
        self.assertTrue(report["proven_minimal"])

    def test_lexicographic_tiebreak(self):
        # Both [1, -1] and [-1, 1] fail zero_sum_pair with equal length
        # and abs-sum; lexicographic order picks [-1, 1].
        report = shrink([2, -2, 9], PREDICATES["zero_sum_pair"], budget=10_000)
        self.assertEqual(report["result"], [-1, 1])
        self.assertTrue(report["proven_minimal"])

    def test_non_failing_input(self):
        # sum([1, 2, 3]) == 6 and shrinking can never increase the sum,
        # so no reachable state fails sum_at_least_ten.
        report = shrink([1, 2, 3], PREDICATES["sum_at_least_ten"], budget=10_000)
        self.assertIsNone(report["result"])
        self.assertTrue(report["proven_minimal"])

    def test_shrinking_can_create_failure_from_passing_input(self):
        # [1, 2, 3] itself passes, but [1, 1] is reachable and fails;
        # the shrinker searches the whole reachable space, not just
        # failing ancestors.
        report = shrink([1, 2, 3], pred_consecutive_equal_nonzero, budget=10_000)
        self.assertEqual(report["result"], [1, 1])
        self.assertTrue(report["proven_minimal"])


class TestBudgetAndFrontier(unittest.TestCase):
    def test_zero_budget_proves_nothing(self):
        report = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=0)
        self.assertFalse(report["proven_minimal"])
        self.assertTrue(report["budget_exhausted"])
        self.assertIsNone(report["result"])
        self.assertGreater(report["frontier_size"], 0)
        self.assertIn("NOT proven", report["note"])

    def test_small_budget_reports_frontier_and_no_false_claim(self):
        report = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=5)
        self.assertFalse(report["proven_minimal"])
        self.assertTrue(report["budget_exhausted"])
        self.assertEqual(report["predicate_calls"], 5)
        self.assertGreater(report["frontier_size"], 0)
        self.assertLessEqual(len(report["frontier_sample"]), 10)
        # Frontier states must be genuine unevaluated neighbours' states:
        # each must be reachable and cheaper than any claim of minimality.
        for sample in report["frontier_sample"]:
            self.assertIn(tuple(sample), reachable_states([4, 4, 9]))

    def test_budget_one_above_need_proves(self):
        tight = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=10_000)
        needed = tight["predicate_calls"]
        exact = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=needed)
        self.assertTrue(exact["proven_minimal"])
        self.assertEqual(exact["result"], [1, 1])
        starved = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=needed - 1)
        self.assertFalse(starved["proven_minimal"])


class TestMemoisation(unittest.TestCase):
    def test_predicate_called_once_per_unique_state(self):
        real_calls = []

        def counting_pred(v):
            real_calls.append(tuple(v))
            return pred_consecutive_equal_nonzero(v)

        report = shrink([4, 4, 9], counting_pred, budget=10_000)
        self.assertEqual(len(real_calls), len(set(real_calls)))  # no duplicates
        self.assertEqual(report["predicate_calls"], len(real_calls))
        self.assertEqual(report["cache_entries"], len(real_calls))

    def test_cache_covers_every_evaluated_state(self):
        report = shrink([4, 4, 9], pred_consecutive_equal_nonzero, budget=10_000)
        self.assertEqual(report["predicate_calls"], report["cache_entries"])


class TestMoveLegality(unittest.TestCase):
    def test_neighbors_are_strictly_cheaper(self):
        for state in reachable_states([2, -3, 1]):
            for nxt in neighbors(state):
                self.assertLess(cost(nxt), cost(state))

    def test_result_is_reachable_from_input(self):
        for vector, name in [
            ([4, 4, 9], "consecutive_equal_nonzero"),
            ([-4, -4, 9], "consecutive_equal_nonzero"),
            ([3, -3, 8], "zero_sum_pair"),
        ]:
            report = shrink(vector, PREDICATES[name], budget=10_000)
            self.assertIn(tuple(report["result"]), reachable_states(vector))


if __name__ == "__main__":
    unittest.main()
