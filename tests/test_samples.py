import json
import unittest

from joinplanner import samples


class SamplesTest(unittest.TestCase):
    def test_all_samples_pass_their_invariants(self):
        results = samples.run_all()
        self.assertEqual(results["low_selectivity_exception"]["planner_gap"], 1.0)
        self.assertGreater(results["low_selectivity_exception"]["greedy_gap"], 5.0)
        self.assertTrue(results["outer_join_barrier"]["multiset_differs"])
        self.assertEqual(results["outer_join_barrier"]["legal_orders"], [["S", "T"]])
        self.assertTrue(results["duplicate_key_spill"]["multiset_matches_reference"])
        self.assertTrue(results["adaptive_replan"]["multiset_matches_reference"])
        self.assertTrue(results["estimate_deviation"]["multiset_matches_reference"])
        self.assertEqual(results["terminal_states"]["budget_status"], "BUDGET_EXHAUSTED")
        self.assertEqual(results["terminal_states"]["cancel_status"], "CANCELLED")
        self.assertTrue(results["terminal_states"]["stale_stats_flag"])

    def test_samples_are_replayable(self):
        first = samples.run_all()
        second = samples.run_all()
        self.assertEqual(
            json.dumps(first, sort_keys=True, default=str),
            json.dumps(second, sort_keys=True, default=str),
        )


if __name__ == "__main__":
    unittest.main()
