import unittest

from relalg import (
    FULL, INNER, LEFT, RIGHT, NULL, UNKNOWN, Join, Project, Relation, Scan,
    Select, and_, col, eq, evaluate, gt, is_null, lit, ne, not_, or_,
    same_multiset, sql_eq, sql_lt, tri_and, tri_not, tri_or,
)


class TestThreeValuedLogic(unittest.TestCase):
    def test_not(self):
        self.assertIs(tri_not(UNKNOWN), UNKNOWN)
        self.assertIs(tri_not(True), False)
        self.assertIs(tri_not(False), True)

    def test_and(self):
        self.assertIs(tri_and(True, True), True)
        self.assertIs(tri_and(True, UNKNOWN), UNKNOWN)
        self.assertIs(tri_and(False, UNKNOWN), False)
        self.assertIs(tri_and(UNKNOWN, UNKNOWN), UNKNOWN)

    def test_or(self):
        self.assertIs(tri_or(False, False), False)
        self.assertIs(tri_or(False, UNKNOWN), UNKNOWN)
        self.assertIs(tri_or(True, UNKNOWN), True)

    def test_unknown_is_not_bool(self):
        with self.assertRaises(TypeError):
            bool(UNKNOWN)


class TestNullComparisons(unittest.TestCase):
    def test_eq_with_null_is_unknown(self):
        self.assertIs(sql_eq(NULL, 1), UNKNOWN)
        self.assertIs(sql_eq(NULL, NULL), UNKNOWN)

    def test_eq_values(self):
        self.assertIs(sql_eq(1, 1), True)
        self.assertIs(sql_eq(1, 2), False)

    def test_lt_with_null_is_unknown(self):
        self.assertIs(sql_lt(NULL, 1), UNKNOWN)
        self.assertIs(sql_lt(1, 2), True)


class TestOperators(unittest.TestCase):
    def test_select_rejects_unknown_rows(self):
        db = {"R": Relation(("A",), ((1,), (NULL,), (0,)))}
        plan = Select(eq(col("A"), lit(1)), Scan("R"))
        out = evaluate(plan, db)
        self.assertEqual(out.rows, ((1,),))

    def test_select_is_null(self):
        db = {"R": Relation(("A",), ((1,), (NULL,), (NULL,)))}
        plan = Select(is_null(col("A")), Scan("R"))
        out = evaluate(plan, db)
        self.assertEqual(out.rows, ((NULL,), (NULL,)))

    def test_project_preserves_duplicates(self):
        db = {"R": Relation(("A", "B"), ((1, 2), (1, 3), (1, 2)))}
        plan = Project(("A",), Scan("R"))
        out = evaluate(plan, db)
        self.assertEqual(out.rows, ((1,), (1,), (1,)))

    def test_inner_join_null_never_matches(self):
        db = {
            "R": Relation(("A", "B"), ((1, NULL),)),
            "S": Relation(("C", "D"), ((NULL, 2),)),
        }
        plan = Join(Scan("R"), Scan("S"), on=(("B", "C"),))
        self.assertEqual(evaluate(plan, db).rows, ())

    def test_inner_join_matches_and_multiplies(self):
        db = {
            "R": Relation(("A", "B"), ((1, 10), (2, 10))),
            "S": Relation(("C", "D"), ((10, 7), (10, 8))),
        }
        plan = Join(Scan("R"), Scan("S"), on=(("B", "C"),))
        out = evaluate(plan, db)
        self.assertEqual(out.schema, ("A", "B", "C", "D"))
        self.assertEqual(len(out.rows), 4)  # 2 x 2 matches

    def test_left_outer_join_pads_null(self):
        db = {
            "R": Relation(("A", "B"), ((1, 10), (2, 20))),
            "S": Relation(("C", "D"), ((10, 7),)),
        }
        plan = Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=LEFT)
        out = evaluate(plan, db)
        self.assertEqual(
            set(out.rows),
            {(1, 10, 10, 7), (2, 20, NULL, NULL)},
        )

    def test_right_outer_join_pads_null(self):
        db = {
            "R": Relation(("A", "B"), ((1, 10),)),
            "S": Relation(("C", "D"), ((10, 7), (30, 9))),
        }
        plan = Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=RIGHT)
        out = evaluate(plan, db)
        self.assertEqual(
            set(out.rows),
            {(1, 10, 10, 7), (NULL, NULL, 30, 9)},
        )

    def test_full_outer_join(self):
        db = {
            "R": Relation(("A", "B"), ((1, 10), (2, 20))),
            "S": Relation(("C", "D"), ((10, 7), (30, 9))),
        }
        plan = Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=FULL)
        out = evaluate(plan, db)
        self.assertEqual(
            set(out.rows),
            {(1, 10, 10, 7), (2, 20, NULL, NULL), (NULL, NULL, 30, 9)},
        )

    def test_composed_predicates(self):
        db = {"R": Relation(("A", "B"), ((1, 5), (2, 1), (3, 9)))}
        pred = and_(gt(col("A"), lit(1)), not_(eq(col("B"), lit(9))))
        plan = Select(pred, Scan("R"))
        self.assertEqual(evaluate(plan, db).rows, ((2, 1),))


class TestMultisetEquality(unittest.TestCase):
    def test_order_is_ignored(self):
        a = Relation(("A",), ((1,), (2,)))
        b = Relation(("A",), ((2,), (1,)))
        self.assertTrue(same_multiset(a, b))

    def test_duplicate_counts_matter(self):
        a = Relation(("A",), ((1,), (1,)))
        b = Relation(("A",), ((1,),))
        self.assertFalse(same_multiset(a, b))

    def test_schema_mismatch(self):
        a = Relation(("A",), ((1,),))
        b = Relation(("B",), ((1,),))
        self.assertFalse(same_multiset(a, b))


if __name__ == "__main__":
    unittest.main()
