"""unittest suite for the IVM engine:  python3.11 -m unittest -v"""

import unittest

from ivm import (
    NULL, UNKNOWN, BaseTable, DuplicateRowIdError, Filter, GroupBy,
    Join, NegativeMultiplicityError, Project, UnknownRowIdError, View,
    sql_and, sql_eq, sql_ge, sql_not, sql_or,
)


class ThreeValuedLogicTests(unittest.TestCase):
    def test_comparisons_with_null_are_unknown(self):
        self.assertIs(sql_eq(NULL, NULL), UNKNOWN)
        self.assertIs(sql_eq(NULL, 1), UNKNOWN)
        self.assertIs(sql_ge(NULL, 30), UNKNOWN)
        self.assertIs(sql_eq(1, 1), True)
        self.assertIs(sql_eq(1, 2), False)

    def test_and_or_not_truth_tables(self):
        self.assertIs(sql_and(True, UNKNOWN), UNKNOWN)
        self.assertIs(sql_and(False, UNKNOWN), False)
        self.assertIs(sql_or(False, UNKNOWN), UNKNOWN)
        self.assertIs(sql_or(True, UNKNOWN), True)
        self.assertIs(sql_not(UNKNOWN), UNKNOWN)
        self.assertIs(sql_not(True), False)


class FilterProjectTests(unittest.TestCase):
    def setUp(self):
        self.t = BaseTable("t", ("a", "b"))

    def test_filter_keeps_only_true(self):
        f = self.t.connect(Filter("f", lambda r: sql_ge(r[1], 10)))
        v = f.connect(View("v"))
        self.t.insert("r1", ("x", 5))      # False -> dropped
        self.t.insert("r2", ("y", NULL))   # UNKNOWN -> dropped
        self.t.insert("r3", ("z", 12))     # True -> kept
        self.assertEqual(v.bag, {("z", 12): 1})
        self.assertEqual(f.bag, {("z", 12): 1})

    def test_projection_multiplicities_accumulate(self):
        p = self.t.connect(Project("p", (0,)))
        v = p.connect(View("v"))
        self.t.insert("r1", ("x", 1))
        self.t.insert("r2", ("x", 2))  # same projected tuple -> multiplicity 2
        self.assertEqual(v.bag, {("x",): 2})
        self.t.delete("r1")
        self.assertEqual(v.bag, {("x",): 1})
        self.t.delete("r2")
        self.assertEqual(v.bag, {})
        # one retraction delta per delete, row gone only after the last one
        self.assertEqual(v.delta_log.count((("x",), -1)), 2)


class JoinTests(unittest.TestCase):
    def setUp(self):
        self.left = BaseTable("l", ("k", "v"))
        self.right = BaseTable("r", ("k", "w"))
        self.join = Join("j", self.left, self.right, (0,), (0,))
        self.view = self.join.connect(View("v"))

    def test_multiplicity_multiplies_with_duplicates(self):
        # two identical left rows (distinct row ids) x three right rows
        self.left.apply([("a", +1, (1, "a")), ("b", +1, (1, "a"))])
        self.right.apply([("x", +1, (1, "x")),
                          ("y", +1, (1, "x")),
                          ("z", +1, (1, "y"))])
        self.assertEqual(self.view.bag,
                         {(1, "a", 1, "x"): 4, (1, "a", 1, "y"): 2})

    def test_retract_last_supporting_row_outputs_delete(self):
        self.left.apply([("a", +1, (1, "a")), ("b", +1, (1, "a"))])
        self.right.insert("x", (1, "x"))
        self.assertEqual(self.view.bag, {(1, "a", 1, "x"): 2})
        self.left.delete("a")  # one supporter left
        self.assertEqual(self.view.bag, {(1, "a", 1, "x"): 1})
        self.left.delete("b")  # last supporter -> row deleted from view
        self.assertEqual(self.view.bag, {})
        self.assertEqual(self.view.delta_log[-1], ((1, "a", 1, "x"), -1))

    def test_null_keys_never_match(self):
        self.left.insert("a", (NULL, "a"))
        self.right.insert("x", (NULL, "x"))  # NULL <> NULL, even both NULL
        self.left.insert("b", (1, "b"))
        self.assertEqual(self.view.bag, {})
        self.right.insert("y", (1, "y"))
        self.assertEqual(self.view.bag, {(1, "b", 1, "y"): 1})

    def test_delete_on_either_side_retracts(self):
        self.left.insert("a", (1, "a"))
        self.right.apply([("x", +1, (1, "x")), ("y", +1, (1, "x"))])
        self.assertEqual(self.view.bag, {(1, "a", 1, "x"): 2})
        self.right.delete("x")
        self.assertEqual(self.view.bag, {(1, "a", 1, "x"): 1})


class GroupByTests(unittest.TestCase):
    def setUp(self):
        self.t = BaseTable("t", ("g", "val"))
        self.g = self.t.connect(
            GroupBy("g", keys=(0,), aggs=(("count", None), ("sum", 1))))
        self.v = self.g.connect(View("v"))

    def test_count_sum_and_group_lifecycle(self):
        self.t.apply([("r1", +1, ("a", 10)), ("r2", +1, ("a", NULL))])
        self.assertEqual(self.v.bag, {("a", 2, 10): 1})  # SUM ignores NULL
        self.t.insert("r3", ("a", 5))
        self.assertEqual(self.v.bag, {("a", 3, 15): 1})
        self.t.delete("r2")
        self.assertEqual(self.v.bag, {("a", 2, 15): 1})
        self.t.delete("r1")
        self.t.delete("r3")  # last supporting row -> group row deleted
        self.assertEqual(self.v.bag, {})
        self.assertEqual(self.v.delta_log[-1], (("a", 1, 5), -1))

    def test_all_null_sum_renders_null(self):
        self.t.apply([("r1", +1, ("a", NULL)), ("r2", +1, ("a", NULL))])
        self.assertEqual(self.v.bag, {("a", 2, NULL): 1})

    def test_sum_returns_to_null_when_last_value_retracted(self):
        self.t.apply([("r1", +1, ("a", 7)), ("r2", +1, ("a", NULL))])
        self.assertEqual(self.v.bag, {("a", 2, 7): 1})
        self.t.delete("r1")
        self.assertEqual(self.v.bag, {("a", 1, NULL): 1})

    def test_null_is_an_ordinary_group_key(self):
        self.t.apply([("r1", +1, (NULL, 1)), ("r2", +1, (NULL, 2))])
        self.assertEqual(self.v.bag, {(NULL, 2, 3): 1})

    def test_count_column_skips_nulls(self):
        t = BaseTable("t2", ("g", "val"))
        g = t.connect(GroupBy("g2", keys=(0,),
                              aggs=(("count", None), ("count", 1))))
        v = g.connect(View("v2"))
        t.apply([("r1", +1, ("a", 1)), ("r2", +1, ("a", NULL))])
        self.assertEqual(v.bag, {("a", 2, 1): 1})


class ErrorTests(unittest.TestCase):
    def test_unknown_row_id(self):
        t = BaseTable("t", ("a",))
        with self.assertRaises(UnknownRowIdError):
            t.delete("nope")

    def test_duplicate_row_id(self):
        t = BaseTable("t", ("a",))
        t.insert("r1", (1,))
        with self.assertRaises(DuplicateRowIdError):
            t.insert("r1", (2,))
        # batch is validated before anything is emitted
        with self.assertRaises(DuplicateRowIdError):
            t.apply([("r2", +1, (3,)), ("r1", +1, (4,))])
        self.assertEqual(set(t.rows), {"r1"})

    def test_negative_multiplicity_is_an_error(self):
        v = View("v")
        with self.assertRaises(NegativeMultiplicityError):
            v.receive([(("ghost",), -1)], None)

    def test_groupby_retraction_of_absent_group_is_an_error(self):
        t = BaseTable("t", ("g",))
        t.connect(GroupBy("g", keys=(0,), aggs=(("count", None),)))
        with self.assertRaises(NegativeMultiplicityError):
            t._emit([(("x",), -1)])


class ReorderTests(unittest.TestCase):
    """Batch order independence: same changes, any order, same final view."""

    def _build(self):
        emp = BaseTable("emp", ("name", "dept", "age", "salary"))
        dept = BaseTable("dept", ("dept", "building"))
        j = Join("j", emp, dept, (1,), (0,))
        f = j.connect(Filter("f", lambda r: sql_ge(r[2], 30)))
        p = f.connect(Project("p", (1, 5, 3)))
        g = p.connect(GroupBy("g", keys=(0, 1),
                              aggs=(("count", None), ("sum", 2))))
        return emp, dept, g.connect(View("v"))

    def test_batch_order_independence(self):
        batches = [
            lambda e, d: d.apply([("d1", +1, ("eng", "A")),
                                  ("d2", +1, ("ops", "B"))]),
            lambda e, d: e.apply([("e1", +1, ("amy", "eng", 34, 100)),
                                  ("e2", +1, ("bob", "eng", 28, 90)),
                                  ("e3", +1, ("cat", "ops", 41, 80))]),
            lambda e, d: e.apply([("e4", +1, ("dan", NULL, 37, 70)),
                                  ("e5", +1, ("eli", "eng", 45, NULL)),
                                  ("e6", +1, ("flo", "ops", 41, 80))]),
            lambda e, d: [e.delete(r) for r in ("e6", "e3")],
        ]
        expected = {("eng", "A", 2, 100): 1}
        orders = [(0, 1, 2, 3), (1, 0, 2, 3), (2, 1, 0, 3), (2, 0, 1, 3)]
        logs = []
        for order in orders:
            e, d, v = self._build()
            for i in order:
                batches[i](e, d)
            self.assertEqual(v.bag, expected, f"order {order}")
            logs.append(v.delta_log)
        # final results agree even though intermediate delta streams differ
        self.assertTrue(any(log != logs[0] for log in logs[1:]))


if __name__ == "__main__":
    unittest.main()
