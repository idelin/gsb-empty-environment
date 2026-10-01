#!/usr/bin/env python3
"""Unit tests for shrinker.py.  All example data is synthetic."""

import json
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from shrinker import PREDICATES, Shrinker, key_of, neighbors

SCRIPT = Path(__file__).resolve().parent / "shrinker.py"


def consecutive_equal_nonzero(vector):
    return any(a == b != 0 for a, b in zip(vector, vector[1:]))


class AcceptanceTest(unittest.TestCase):
    """Acceptance boundary from the task statement."""

    def test_449_shrinks_to_1_1_not_minus_1_1(self):
        shrinker = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=10_000)
        report = shrinker.shrink([4, 4, 9])
        # Operations may not flip signs, so [-1, -1] is unreachable from
        # [4, 4, 9]; the proven global minimum must be [1, 1].
        self.assertEqual(report["result"], [1, 1])
        self.assertNotEqual(report["result"], [-1, -1])
        self.assertTrue(report["proven_minimal"])
        self.assertEqual(report["unexplored_frontier"], [])
        self.assertTrue(report["input_fails"])

    def test_negative_input_shrinks_to_minus_1_minus_1(self):
        shrinker = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=10_000)
        report = shrinker.shrink([-4, -4, 9])
        self.assertEqual(report["result"], [-1, -1])
        self.assertTrue(report["proven_minimal"])

    def test_sign_is_never_flipped_anywhere(self):
        shrinker = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=10_000)
        shrinker.shrink([4, 4, 9])
        # Every vector ever tested (the whole cache) stays non-negative,
        # because the input is non-negative and reduction cannot cross zero.
        for vector in shrinker.cache:
            self.assertTrue(all(x >= 0 for x in vector), vector)


class CorrectnessTest(unittest.TestCase):
    def test_global_minimum_not_local_minimum(self):
        # Failing set {[1,1], [3,3]}: every one-step neighbour of [3,3]
        # passes, so a greedy local search would get stuck at [3,3].
        # The global minimum over the reachable space is [1,1].
        pred = lambda v: v in ([1, 1], [3, 3])
        report = Shrinker(pred, budget=10_000).shrink([3, 3])
        self.assertEqual(report["result"], [1, 1])
        self.assertTrue(report["proven_minimal"])

    def test_shrink_to_empty_vector(self):
        # Every vector fails, so the empty vector is the global minimum.
        report = Shrinker(lambda v: True, budget=1_000).shrink([3, -1])
        self.assertEqual(report["result"], [])
        self.assertTrue(report["proven_minimal"])

    def test_lexicographic_tiebreak(self):
        # Both [0,2] and [2,0] fail and share key (2, 2, ...); the
        # lexicographically smaller [0,2] must win.
        pred = lambda v: v in ([2, 2], [0, 2], [2, 0])
        report = Shrinker(pred, budget=10_000).shrink([2, 2])
        self.assertEqual(report["result"], [0, 2])
        self.assertTrue(report["proven_minimal"])

    def test_length_dominates_sum(self):
        # [9] has a larger absolute sum than [1,1] but a smaller length,
        # so a predicate that fails on both must yield [9].
        pred = lambda v: v in ([4, 4, 9], [9], [1, 1])
        report = Shrinker(pred, budget=10_000).shrink([4, 4, 9])
        self.assertEqual(report["result"], [9])
        self.assertTrue(report["proven_minimal"])

    def test_deterministic_repeated_runs(self):
        first = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=10_000).shrink([4, 4, 9])
        second = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=10_000).shrink([4, 4, 9])
        self.assertEqual(first, second)

    def test_neighbors_only_delete_or_reduce_abs(self):
        nbs = list(neighbors((2, -3, 0)))
        self.assertIn((1, -3, 0), nbs)
        self.assertIn((2, -2, 0), nbs)
        self.assertIn((-3, 0), nbs)
        self.assertIn((2, 0), nbs)
        self.assertIn((2, -3), nbs)
        self.assertNotIn((2, -4, 0), nbs)
        self.assertNotIn((-2, -3, 0), nbs)
        self.assertNotIn((2, -3, 1), nbs)
        self.assertEqual(list(neighbors(())), [])


class CacheAndBudgetTest(unittest.TestCase):
    def test_no_duplicate_predicate_evaluations(self):
        evaluated = []

        def counting_pred(vector):
            evaluated.append(tuple(vector))
            return consecutive_equal_nonzero(vector)

        shrinker = Shrinker(counting_pred, budget=10_000)
        shrinker.shrink([4, 4, 9])
        self.assertEqual(len(evaluated), len(set(evaluated)))
        self.assertEqual(shrinker.calls, len(shrinker.cache))
        self.assertLessEqual(shrinker.calls, shrinker.budget)

    def test_cache_is_shared_across_shrink_calls(self):
        evaluated = []

        def counting_pred(vector):
            evaluated.append(tuple(vector))
            return consecutive_equal_nonzero(vector)

        shrinker = Shrinker(counting_pred, budget=10_000)
        shrinker.shrink([4, 4])
        calls_after_first = shrinker.calls
        shrinker.shrink([4, 4, 9])
        # The second run reuses cached results instead of re-evaluating.
        self.assertGreater(shrinker.cache_hits, 0)
        self.assertEqual(len(evaluated), len(set(evaluated)))
        self.assertLess(shrinker.calls, calls_after_first + 400)

    def test_budget_exhaustion_reports_unproven_with_frontier(self):
        shrinker = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=1)
        report = shrinker.shrink([4, 4, 9])
        self.assertEqual(report["result"], [4, 4, 9])
        self.assertFalse(report["proven_minimal"])
        self.assertTrue(report["budget_exhausted"])
        self.assertEqual(report["predicate_calls"], 1)
        self.assertGreater(report["unexplored_frontier_total"], 0)
        result_key = key_of(report["result"])
        for state in report["unexplored_frontier"]:
            self.assertLess(key_of(state), result_key)
        self.assertTrue(any("NOT proven" in note for note in report["notes"]))

    def test_zero_budget_returns_input_unverified(self):
        report = Shrinker(PREDICATES["consecutive_equal_nonzero"], budget=0).shrink([4, 4, 9])
        self.assertIsNone(report["input_fails"])
        self.assertEqual(report["result"], [4, 4, 9])
        self.assertFalse(report["proven_minimal"])
        self.assertEqual(report["predicate_calls"], 0)
        self.assertEqual(report["unexplored_frontier"], [[4, 4, 9]])

    def test_expansion_cap_reports_unproven(self):
        shrinker = Shrinker(
            PREDICATES["consecutive_equal_nonzero"], budget=10_000, max_expansions=1
        )
        report = shrinker.shrink([4, 4, 9])
        self.assertFalse(report["proven_minimal"])
        self.assertTrue(report["expansion_capped"])
        self.assertFalse(report["frontier_complete"])

    def test_non_failing_input_returned_unchanged(self):
        report = Shrinker(PREDICATES["any_negative"], budget=100).shrink([1, 2])
        self.assertFalse(report["input_fails"])
        self.assertEqual(report["result"], [1, 2])
        self.assertFalse(report["proven_minimal"])
        self.assertEqual(report["unexplored_frontier"], [])


class CliTest(unittest.TestCase):
    def run_cli(self, args, stdin_text=None):
        return subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            input=stdin_text,
            capture_output=True,
            text=True,
            check=True,
        )

    def test_cli_with_vector_flag(self):
        proc = self.run_cli(
            ["--predicate", "consecutive_equal_nonzero", "--vector", "[4,4,9]"]
        )
        report = json.loads(proc.stdout)
        self.assertEqual(report["result"], [1, 1])
        self.assertTrue(report["proven_minimal"])
        self.assertEqual(report["predicate"], "consecutive_equal_nonzero")

    def test_cli_with_stdin_object(self):
        proc = self.run_cli(
            [],
            stdin_text=json.dumps(
                {
                    "vector": [4, 4, 9],
                    "predicate": "consecutive_equal_nonzero",
                    "budget": 10_000,
                }
            ),
        )
        report = json.loads(proc.stdout)
        self.assertEqual(report["result"], [1, 1])
        self.assertTrue(report["proven_minimal"])

    def test_cli_with_stdin_bare_array(self):
        proc = self.run_cli(
            ["--predicate", "consecutive_equal_nonzero"], stdin_text="[-4, -4, 9]"
        )
        report = json.loads(proc.stdout)
        self.assertEqual(report["result"], [-1, -1])

    def test_cli_rejects_unknown_predicate(self):
        proc = subprocess.run(
            [sys.executable, str(SCRIPT), "--predicate", "nope", "--vector", "[1]"],
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(proc.returncode, 0)


if __name__ == "__main__":
    unittest.main()
