import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minirel import (Aggregate, Budget, BudgetTracker, Executor, Filter,
                     HashJoin, NULL, Project, Scan, Table, is_null,
                     sql_and, sql_eq, sql_not, sql_or)
from minirel.table import sql_lt


class NullLogicTest(unittest.TestCase):
    def test_three_valued_logic(self):
        self.assertIs(sql_and(True, NULL), NULL)
        self.assertIs(sql_and(False, NULL), False)
        self.assertIs(sql_or(True, NULL), True)
        self.assertIs(sql_or(False, NULL), NULL)
        self.assertIs(sql_not(NULL), NULL)
        self.assertIs(sql_eq(NULL, NULL), NULL)
        self.assertIs(sql_eq(1, NULL), NULL)
        self.assertIs(sql_lt(NULL, 5), NULL)

    def test_null_has_no_truth_value(self):
        with self.assertRaises(TypeError):
            bool(NULL)

    def test_filter_rejects_unknown(self):
        table = Table("t", ("a", "b"), [(1, 10), (2, NULL), (3, 30)])
        tracker = BudgetTracker(Budget())
        filt = Filter(Scan(table, tracker),
                      lambda r: sql_lt(r[1], 20))
        result = Executor(filt, tracker).run()
        self.assertEqual(result.rows, [(1, 10)])  # NULL row filtered out


class JoinNullTest(unittest.TestCase):
    def test_null_keys_never_match(self):
        left = Table("l", ("id", "k"), [(1, 10), (2, NULL)])
        right = Table("r", ("k", "v"), [(10, "x"), (NULL, "y")])
        tracker = BudgetTracker(Budget())
        join = HashJoin(Scan(left, tracker), Scan(right, tracker),
                        left_key=lambda r: r[1], right_key=lambda r: r[0],
                        tracker=tracker)
        result = Executor(join, tracker).run()
        self.assertEqual(result.rows, [(1, 10, 10, "x")])


class MultisetTest(unittest.TestCase):
    def test_scan_preserves_duplicates(self):
        table = Table("t", ("a",), [(1,), (1,), (2,)])
        tracker = BudgetTracker(Budget())
        result = Executor(Scan(table, tracker), tracker).run()
        self.assertEqual(result.rows, [(1,), (1,), (2,)])

    def test_join_multiplies_multiplicities(self):
        left = Table("l", ("k",), [(1,), (1,)])      # 2x key 1
        right = Table("r", ("k",), [(1,), (1,), (1,)])  # 3x key 1
        tracker = BudgetTracker(Budget())
        join = HashJoin(Scan(left, tracker), Scan(right, tracker),
                        left_key=lambda r: r[0], right_key=lambda r: r[0],
                        tracker=tracker)
        result = Executor(join, tracker).run()
        self.assertEqual(len(result.rows), 6)  # 2 * 3, no dedup


class AggregateNullTest(unittest.TestCase):
    def test_count_star_vs_count_col_and_null_aggregates(self):
        table = Table("t", ("g", "v"),
                      [("a", 1), ("a", NULL), ("b", NULL), ("b", NULL)])
        tracker = BudgetTracker(Budget())
        agg = Aggregate(Scan(table, tracker),
                        group_keys=[lambda r: r[0]],
                        aggs=[("count", None), ("count", 1),
                              ("sum", 1), ("avg", 1)],
                        tracker=tracker)
        result = Executor(agg, tracker).run()
        rows = {r[0]: r for r in result.rows}
        self.assertEqual(rows["a"], ("a", 2, 1, 1, 1.0))
        # all-NULL group: COUNT(*)=2, COUNT(v)=0, SUM/AVG -> NULL
        self.assertEqual(rows["b"][1], 2)
        self.assertEqual(rows["b"][2], 0)
        self.assertTrue(is_null(rows["b"][3]))
        self.assertTrue(is_null(rows["b"][4]))

    def test_null_group_key_groups_together(self):
        table = Table("t", ("g", "v"), [(NULL, 1), (NULL, 2), (7, 3)])
        tracker = BudgetTracker(Budget())
        agg = Aggregate(Scan(table, tracker),
                        group_keys=[lambda r: r[0]],
                        aggs=[("count", None)],
                        tracker=tracker)
        result = Executor(agg, tracker).run()
        self.assertEqual(sorted(result.rows, key=str),
                         [(7, 1), (NULL, 2)])


if __name__ == "__main__":
    unittest.main()
