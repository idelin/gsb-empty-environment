import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minirel import (Aggregate, Budget, BudgetTracker, Executor, HashJoin,
                     NULL, PartialAggregate, Scan, Table)

EMP = Table(
    "employees",
    ("emp_id", "dept_id", "salary"),
    [(1, 10, 90000), (2, 10, NULL), (3, 20, 80000),
     (4, 20, 80000), (5, 30, 60000)],
)


def agg_plan(tracker):
    return Aggregate(
        Scan(EMP, tracker),
        group_keys=[lambda r: r[1]],
        aggs=[("count", None), ("sum", 2)],
        tracker=tracker,
    )


class RowBudgetTest(unittest.TestCase):
    def test_row_budget_interrupts_with_partial_stats(self):
        tracker = BudgetTracker(Budget(max_rows=3))
        result = Executor(agg_plan(tracker), tracker).run()
        self.assertEqual(result.status, "budget")
        self.assertEqual(result.stats.budget_kind, "rows")
        self.assertEqual(result.stats.budget_limit, 3)
        self.assertGreater(result.stats.rows, 0)
        self.assertEqual(result.rows, [])

    def test_partial_aggregate_is_marked_incomplete(self):
        tracker = BudgetTracker(Budget(max_rows=3))
        result = Executor(agg_plan(tracker), tracker).run()
        partial = result.partial
        self.assertIsInstance(partial, PartialAggregate)
        self.assertFalse(partial.complete)
        # partial state reflects only the rows seen so far
        self.assertEqual(dict((r[0], r[1]) for r in partial.rows)[10], 2)

    def test_partial_aggregate_cannot_masquerade_as_final(self):
        with self.assertRaises(ValueError):
            PartialAggregate(rows=[], complete=True)

    def test_full_run_has_no_partial(self):
        tracker = BudgetTracker(Budget())
        result = Executor(agg_plan(tracker), tracker).run()
        self.assertEqual(result.status, "complete")
        self.assertIsNone(result.partial)
        self.assertIn((10, 2, 90000), result.rows)


class StepBudgetTest(unittest.TestCase):
    def test_step_budget_interrupts(self):
        tracker = BudgetTracker(Budget(max_steps=2))
        result = Executor(agg_plan(tracker), tracker).run()
        self.assertEqual(result.status, "budget")
        self.assertEqual(result.stats.budget_kind, "steps")
        self.assertEqual(result.stats.steps, 3)


class MemoryBudgetTest(unittest.TestCase):
    def test_join_build_side_memory_budget(self):
        tracker = BudgetTracker(Budget(max_memory=128))  # 2 rows * 64B
        join = HashJoin(Scan(EMP, tracker), Scan(EMP, tracker),
                        left_key=lambda r: r[0], right_key=lambda r: r[0],
                        tracker=tracker)
        result = Executor(join, tracker).run()
        self.assertEqual(result.status, "budget")
        self.assertEqual(result.stats.budget_kind, "memory")

    def test_memory_released_after_close(self):
        tracker = BudgetTracker(Budget())
        join = HashJoin(Scan(EMP, tracker), Scan(EMP, tracker),
                        left_key=lambda r: r[0], right_key=lambda r: r[0],
                        tracker=tracker)
        Executor(join, tracker).run()
        self.assertEqual(tracker.memory, 0)
        self.assertGreater(tracker.peak_memory, 0)


if __name__ == "__main__":
    unittest.main()
