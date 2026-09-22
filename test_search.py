"""Tests for input generation, multiset comparison, counterexample
search, and minimization."""
import unittest

from generate import database_size, enum_databases, enum_tables
from relalg import NULL, Col, Cmp, Join, LeftJoin, Lit, Project, Scan, Select
from search import (find_counterexample, is_counterexample, minimize,
                    results_equal)

CAT = {"R": ["a"], "S": ["c", "b"]}
DOMAIN = [0, 1, NULL]
COND = Cmp("=", Col("a"), Col("c"))
PRED_B = Cmp(">", Col("b"), Lit(0))
PRED_A = Cmp(">", Col("a"), Lit(0))

# Not equivalent: filter pushed through a left outer join.
LOJ_P1 = Select(PRED_B, LeftJoin(COND, Scan("R"), Scan("S")))
LOJ_P2 = LeftJoin(COND, Scan("R"), Select(PRED_B, Scan("S")))
# Equivalent: left-only filter pushed into an inner join.
IJ_P1 = Select(PRED_A, Join(COND, Scan("R"), Scan("S")))
IJ_P2 = Join(COND, Select(PRED_A, Scan("R")), Scan("S"))


class TestGenerator(unittest.TestCase):
    def test_enum_tables_counts(self):
        # domain 3, arity 1 -> rows: 3; tables: 1 + 3 + C(3+2-1,2)=6 -> 10
        self.assertEqual(len(enum_tables(1, DOMAIN, 2)), 10)
        # arity 2 -> 9 rows; tables: 1 + 9 + C(10,2)=45 -> 55
        self.assertEqual(len(enum_tables(2, DOMAIN, 2)), 55)

    def test_enum_databases_total(self):
        dbs = list(enum_databases(CAT, DOMAIN, 2))
        self.assertEqual(len(dbs), 10 * 55)

    def test_small_inputs_first(self):
        dbs = enum_databases(CAT, DOMAIN, 2)
        first = next(dbs)
        self.assertEqual(database_size(first), 0)


class TestMultisetComparison(unittest.TestCase):
    def test_row_order_irrelevant(self):
        db = {"R": [(1,), (0,)], "S": []}
        p1 = Scan("R")
        p2 = Select(Or_true(), Scan("R"))
        self.assertTrue(results_equal(p1, p2, CAT, db))

    def test_duplicate_count_difference_is_a_difference(self):
        # Same rows as a set, different multiplicities -> counterexample.
        db = {"R": [(0,)], "S": [(0, 1), (0, 2)]}
        p1 = Project(("a",), Join(COND, Scan("R"), Scan("S")))  # {(0),(0)}
        p2 = Project(("a",), Scan("R"))                          # {(0)}
        self.assertTrue(is_counterexample(p1, p2, CAT, db))

    def test_schema_mismatch_is_a_difference(self):
        db = {"R": [(1,)], "S": []}
        self.assertTrue(is_counterexample(Scan("R"),
                                          Project(("a", "a"), Scan("R")),
                                          CAT, db))


def Or_true():
    from relalg import Or, IsNull, Not
    return Or(IsNull(Col("a")), Not(IsNull(Col("a"))))


class TestSearch(unittest.TestCase):
    def test_finds_counterexample_for_outer_join_pair(self):
        res = find_counterexample(LOJ_P1, LOJ_P2, CAT, DOMAIN, 2,
                                  budget=100_000)
        self.assertEqual(res.status, "counterexample")
        self.assertTrue(res.found)
        self.assertIsNotNone(res.counterexample)

    def test_counterexample_is_minimal(self):
        res = find_counterexample(LOJ_P1, LOJ_P2, CAT, DOMAIN, 2,
                                  budget=100_000)
        ce = res.counterexample
        # The known minimal CE: one row in R, empty S (filter after LOJ
        # drops the NULL-padded row; filter before LOJ keeps it).
        self.assertEqual(database_size(ce), 1)
        self.assertEqual(ce["S"], [])
        self.assertEqual(len(ce["R"]), 1)
        # 1-minimality: removing any row makes the plans agree.
        for name in ce:
            for i in range(len(ce[name])):
                smaller = dict(ce, **{name: ce[name][:i] + ce[name][i+1:]})
                self.assertFalse(is_counterexample(LOJ_P1, LOJ_P2, CAT, smaller))

    def test_equivalent_pair_exhausts_space_without_ce(self):
        res = find_counterexample(IJ_P1, IJ_P2, CAT, DOMAIN, 2,
                                  budget=100_000)
        self.assertEqual(res.status, "space_exhausted")
        self.assertFalse(res.found)
        self.assertEqual(res.checked, 550)
        self.assertIn("not a proof", res.note.lower()
                      .replace("checked", "checked; not a proof"))

    def test_budget_cutoff_is_not_a_proof(self):
        res = find_counterexample(IJ_P1, IJ_P2, CAT, DOMAIN, 2, budget=7)
        self.assertEqual(res.status, "budget_exhausted")
        self.assertEqual(res.checked, 7)
        self.assertFalse(res.found)

    def test_time_budget_cutoff(self):
        res = find_counterexample(IJ_P1, IJ_P2, CAT, DOMAIN, 2,
                                  budget=10**9, time_budget=0.0)
        self.assertEqual(res.status, "budget_exhausted")

    def test_minimize_shrinks_large_counterexample(self):
        big = {"R": [(0,), (1,), (NULL,)], "S": [(0, 0), (1, 1)]}
        self.assertTrue(is_counterexample(LOJ_P1, LOJ_P2, CAT, big))
        small = minimize(LOJ_P1, LOJ_P2, CAT, big)
        self.assertLess(database_size(small), database_size(big))
        self.assertTrue(is_counterexample(LOJ_P1, LOJ_P2, CAT, small))
        for name in small:
            for i in range(len(small[name])):
                smaller = dict(small, **{name: small[name][:i] + small[name][i+1:]})
                self.assertFalse(is_counterexample(LOJ_P1, LOJ_P2, CAT, smaller))


if __name__ == "__main__":
    unittest.main()
