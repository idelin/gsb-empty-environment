import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minirel import (Budget, BudgetTracker, Executor, Limit, Scan, Sort,
                     Table)

BIG = Table("big", ("k", "v"), [(i, "r%d" % i) for i in range(500)])


def make(tracker, **kw):
    return Sort(Scan(BIG, tracker), key=lambda r: r[0],
                tracker=tracker, tempdir=kw["tmp"], spill_after=100)


class TerminationTest(unittest.TestCase):
    def test_limit_is_normal_early_termination(self):
        tracker = BudgetTracker(Budget())
        result = Executor(Limit(Scan(BIG, tracker), 3), tracker).run()
        self.assertEqual(result.status, "limit")
        self.assertIsNone(result.message)
        self.assertIsNone(result.partial)
        self.assertEqual(len(result.rows), 3)
        # the child was stopped early: fewer rows than the table paid for
        self.assertLessEqual(result.stats.rows, 3)

    def test_complete_is_distinct_from_limit(self):
        tracker = BudgetTracker(Budget())
        result = Executor(Limit(Scan(BIG, tracker), 10_000), tracker).run()
        self.assertEqual(result.status, "complete")
        self.assertEqual(len(result.rows), 500)

    def test_budget_interrupt_is_distinct_from_limit(self):
        tracker = BudgetTracker(Budget(max_rows=5))
        result = Executor(Limit(Scan(BIG, tracker), 3), tracker).run()
        # limit 3 is reachable before the row budget of 5 -> limit wins
        self.assertEqual(result.status, "limit")

        tracker = BudgetTracker(Budget(max_rows=2))
        result = Executor(Limit(Scan(BIG, tracker), 3), tracker).run()
        # budget blows up before limit can be satisfied
        self.assertEqual(result.status, "budget")
        self.assertEqual(result.stats.budget_kind, "rows")

    def test_sort_runs_complete_and_deletes_spills(self):
        tracker = BudgetTracker(Budget())
        with tempfile.TemporaryDirectory() as tmp:
            result = Executor(make(tracker, tmp=tmp), tracker).run()
            self.assertEqual(result.status, "complete")
            self.assertEqual([r[0] for r in result.rows],
                             list(range(500)))
            self.assertEqual(os.listdir(tmp), [])


if __name__ == "__main__":
    unittest.main()
