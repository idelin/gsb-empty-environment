"""Tests for NULL / three-valued-logic / bag semantics of the evaluator."""
import unittest
from collections import Counter

from relalg import (NULL, UNKNOWN, And, Col, Cmp, IsNull, Join, LeftJoin, Lit,
                    Not, Or, Project, Scan, Select, evaluate,
                    tv_and, tv_not, tv_or)

CAT = {"R": ["a"], "S": ["c", "b"]}


class TestNull(unittest.TestCase):
    def test_singleton_and_repr(self):
        self.assertIs(NULL, type(NULL)())
        self.assertEqual(repr(NULL), "NULL")

    def test_no_truth_value(self):
        with self.assertRaises(TypeError):
            bool(NULL)

    def test_deepcopy_safe(self):
        import copy
        self.assertIs(copy.deepcopy(NULL), NULL)


class TestThreeValuedLogic(unittest.TestCase):
    def test_not(self):
        self.assertIs(tv_not(UNKNOWN), UNKNOWN)
        self.assertIs(tv_not(True), False)
        self.assertIs(tv_not(False), True)

    def test_and(self):
        self.assertIs(tv_and(True, UNKNOWN), UNKNOWN)
        self.assertIs(tv_and(False, UNKNOWN), False)
        self.assertIs(tv_and(True, True), True)

    def test_or(self):
        self.assertIs(tv_or(False, UNKNOWN), UNKNOWN)
        self.assertIs(tv_or(True, UNKNOWN), True)
        self.assertIs(tv_or(False, False), False)

    def test_cmp_with_null_is_unknown(self):
        env = {"x": NULL}
        for op in ("=", "!=", "<", "<=", ">", ">="):
            self.assertIs(Cmp(op, Col("x"), Lit(1)).eval(env), UNKNOWN)
        self.assertIs(Cmp("=", Lit(NULL), Lit(NULL)).eval({}), UNKNOWN)

    def test_isnull(self):
        self.assertIs(IsNull(Col("x")).eval({"x": NULL}), True)
        self.assertIs(IsNull(Col("x")).eval({"x": 0}), False)

    def test_and_or_not_exprs(self):
        env = {"x": 1, "y": NULL}
        e = And(Cmp(">", Col("x"), Lit(0)), Cmp("=", Col("y"), Lit(1)))
        self.assertIs(e.eval(env), UNKNOWN)
        e2 = Or(Cmp(">", Col("x"), Lit(0)), Not(IsNull(Col("y"))))
        self.assertIs(e2.eval(env), True)


class TestOperators(unittest.TestCase):
    def test_select_keeps_only_true(self):
        db = {"R": [(0,), (1,), (NULL,)], "S": []}
        plan = Select(Cmp(">", Col("a"), Lit(0)), Scan("R"))
        _, rows = evaluate(plan, CAT, db)
        self.assertEqual(rows, [(1,)])  # 0 -> FALSE, NULL -> UNKNOWN dropped

    def test_project_is_bag(self):
        db = {"R": [(1,), (1,), (2,)], "S": []}
        _, rows = evaluate(Project(("a",), Scan("R")), CAT, db)
        self.assertEqual(Counter(rows), Counter({(1,): 2, (2,): 1}))

    def test_inner_join_null_keys_never_match(self):
        db = {"R": [(NULL,), (1,)], "S": [(NULL, 9), (1, 8)]}
        plan = Join(Cmp("=", Col("a"), Col("c")), Scan("R"), Scan("S"))
        _, rows = evaluate(plan, CAT, db)
        self.assertEqual(rows, [(1, 1, 8)])

    def test_inner_join_multiplicity(self):
        db = {"R": [(1,), (1,)], "S": [(1, 7), (1, 8)]}
        plan = Join(Cmp("=", Col("a"), Col("c")), Scan("R"), Scan("S"))
        _, rows = evaluate(plan, CAT, db)
        self.assertEqual(len(rows), 4)  # 2 x 2 under bag semantics

    def test_left_join_pads_nulls(self):
        db = {"R": [(1,), (2,)], "S": [(1, 7)]}
        plan = LeftJoin(Cmp("=", Col("a"), Col("c")), Scan("R"), Scan("S"))
        schema, rows = evaluate(plan, CAT, db)
        self.assertEqual(schema, ["a", "c", "b"])
        self.assertEqual(Counter(rows),
                         Counter({(1, 1, 7): 1, (2, NULL, NULL): 1}))

    def test_left_join_unmatched_null_key_still_padded(self):
        db = {"R": [(NULL,)], "S": [(NULL, 7)]}
        plan = LeftJoin(Cmp("=", Col("a"), Col("c")), Scan("R"), Scan("S"))
        _, rows = evaluate(plan, CAT, db)
        self.assertEqual(rows, [(NULL, NULL, NULL)])

    def test_ambiguous_join_columns_rejected(self):
        cat = {"A": ["x"], "B": ["x"]}
        db = {"A": [(1,)], "B": [(1,)]}
        plan = Join(Cmp("=", Col("x"), Col("x")), Scan("A"), Scan("B"))
        with self.assertRaises(ValueError):
            evaluate(plan, cat, db)


if __name__ == "__main__":
    unittest.main()
