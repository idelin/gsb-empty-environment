import copy
import unittest

from cardian import analyze, verify_witness
from reference.enumerator import brute_force

from helpers import RESUMABLE_QUERY, RESUMABLE_STATS

FULL_BUDGET = 1_000_000


class TestBudgetInterruption(unittest.TestCase):
    def test_budget_exhaustion_never_claims_exact(self):
        res = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=3)
        self.assertTrue(res["consistent"])
        self.assertFalse(res["exact"])
        self.assertIn("state", res)
        self.assertLessEqual(res["search"]["nodes"], 3)
        self.assertGreater(res["search"]["frontier_remaining"], 0)

    def test_proven_bounds_cover_unsearched_branches(self):
        ref = brute_force(RESUMABLE_STATS, RESUMABLE_QUERY)
        for budget in (1, 2, 5, 11, 40):
            res = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=budget)
            if not res.get("consistent"):
                continue
            # proven bounds must bracket the true optimum even when the
            # search was interrupted
            self.assertLessEqual(res["min"]["lower"], ref["min"])
            self.assertGreaterEqual(res["max"]["upper"], ref["max"])
            if res["min"]["upper"] is not None:
                self.assertGreaterEqual(res["min"]["upper"], ref["min"])
                self.assertLessEqual(res["max"]["lower"], ref["max"])
                # current feasible witnesses must be genuine
                for side in ("min", "max"):
                    verdict = verify_witness(
                        RESUMABLE_STATS, RESUMABLE_QUERY, res[side]["witness"])
                    self.assertTrue(verdict["ok"], verdict["errors"])

    def test_counters_recorded(self):
        res = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=FULL_BUDGET)
        s = res["search"]
        for key in ("nodes", "leaves", "pruned_infeasible", "pruned_bound"):
            self.assertIn(key, s)
        self.assertGreater(s["nodes"], 0)
        self.assertGreater(s["leaves"], 0)


class TestResume(unittest.TestCase):
    def test_resume_matches_uninterrupted_run(self):
        interrupted = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=10)
        self.assertFalse(interrupted["exact"])
        state = interrupted["state"]

        resumed = analyze(RESUMABLE_STATS, RESUMABLE_QUERY,
                          budget=FULL_BUDGET, state=state)
        fresh = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=FULL_BUDGET)

        self.assertTrue(resumed["exact"])
        self.assertTrue(fresh["exact"])
        # optimal bounds identical whether or not the search was resumed
        self.assertEqual(resumed["min"]["lower"], fresh["min"]["lower"])
        self.assertEqual(resumed["max"]["upper"], fresh["max"]["upper"])
        self.assertEqual(resumed["min"]["witness"]["claim"],
                         fresh["min"]["witness"]["claim"])
        self.assertEqual(resumed["max"]["witness"]["claim"],
                         fresh["max"]["witness"]["claim"])
        # interrupted proven bounds must bracket the final optima
        self.assertLessEqual(interrupted["min"]["lower"], fresh["min"]["lower"])
        self.assertGreaterEqual(interrupted["max"]["upper"], fresh["max"]["upper"])

    def test_stale_state_rejected(self):
        interrupted = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=5)
        state = interrupted["state"]
        modified = copy.deepcopy(RESUMABLE_STATS)
        modified["tables"]["S"]["row_count"] = 4
        with self.assertRaises(ValueError):
            analyze(modified, RESUMABLE_QUERY, budget=FULL_BUDGET, state=state)

    def test_determinism(self):
        a = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=FULL_BUDGET)
        b = analyze(RESUMABLE_STATS, RESUMABLE_QUERY, budget=FULL_BUDGET)
        self.assertEqual(a["min"]["witness"], b["min"]["witness"])
        self.assertEqual(a["max"]["witness"], b["max"]["witness"])
        self.assertEqual(a["search"]["nodes"], b["search"]["nodes"])


if __name__ == "__main__":
    unittest.main()
