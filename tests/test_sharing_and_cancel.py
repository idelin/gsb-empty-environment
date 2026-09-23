import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minirel import (Budget, BudgetTracker, Cancelled, Executor, Filter,
                     Scan, SharedSubplan, Sort, Table, sql_eq)


class SharedSubplanTest(unittest.TestCase):
    def test_shared_scan_charges_rows_once_but_serves_both_consumers(self):
        table = Table("t", ("id", "dept"),
                      [(1, 10), (2, 20), (3, 10), (4, 20)])
        tracker = BudgetTracker(Budget())
        shared = SharedSubplan(Scan(table, tracker), tracker)
        left = Filter(shared.consumer(), lambda r: sql_eq(r[1], 10))
        right = Filter(shared.consumer(), lambda r: sql_eq(r[1], 20))

        left_rows, right_rows = [], []
        left.open()
        right.open()
        while True:
            row = left.next()
            if row is None:
                break
            left_rows.append(row)
        while True:
            row = right.next()
            if row is None:
                break
            right_rows.append(row)
        left.close()  # closes the shared child exactly once

        self.assertEqual(len(left_rows), 2)
        self.assertEqual(len(right_rows), 2)
        # both consumers saw all data, but rows were charged once
        self.assertEqual(tracker.rows, table.cardinality())
        # one next()-step per row plus the single end-of-stream probe
        self.assertEqual(tracker.steps, table.cardinality() + 1)

    def test_shared_scan_under_row_budget_still_accounts(self):
        table = Table("t", ("id", "dept"),
                      [(1, 10), (2, 20), (3, 10), (4, 20)])
        tracker = BudgetTracker(Budget(max_rows=2))
        shared = SharedSubplan(Scan(table, tracker), tracker)
        consumer = shared.consumer()
        consumer.open()
        got = []
        with self.assertRaises(Exception):
            while True:
                row = consumer.next()
                if row is None:
                    break
                got.append(row)
        # the breaching row is charged too, so partial stats show the
        # overshoot (limit 2, used 3); no row accounting was dropped
        self.assertEqual(tracker.rows, 3)
        consumer.close()
        self.assertEqual(tracker.memory, 0)


class CancellingTracker(BudgetTracker):
    """Deterministically cancels after ``after`` cancellation checks."""

    def __init__(self, budget, after):
        super().__init__(budget)
        self.after = after
        self.checks = 0

    def check_cancelled(self):
        self.checks += 1
        if self.checks >= self.after:
            self.cancel()
        super().check_cancelled()


class CancelTest(unittest.TestCase):
    def test_cancelled_sort_releases_spill_files_and_memory(self):
        big = Table("big", ("k", "v"),
                    [(i, "r%d" % i) for i in range(5000)])
        tracker = CancellingTracker(Budget(), after=550)
        with tempfile.TemporaryDirectory() as tmp:
            sort = Sort(Scan(big, tracker), key=lambda r: r[0],
                        tracker=tracker, tempdir=tmp, spill_after=200)
            result = Executor(sort, tracker).run()
            self.assertEqual(result.status, "cancelled")
            self.assertEqual(tracker.memory, 0)
            self.assertEqual(os.listdir(tmp), [])

    def test_cancel_before_start(self):
        table = Table("t", ("a",), [(1,), (2,)])
        tracker = BudgetTracker(Budget())
        tracker.cancel()
        result = Executor(Scan(table, tracker), tracker).run()
        self.assertEqual(result.status, "cancelled")
        self.assertEqual(result.rows, [])

    def test_cancelled_is_distinct_exception(self):
        tracker = BudgetTracker(Budget())
        tracker.cancel()
        with self.assertRaises(Cancelled):
            tracker.check_cancelled()


if __name__ == "__main__":
    unittest.main()
