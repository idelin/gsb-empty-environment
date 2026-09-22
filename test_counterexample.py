import unittest

from counterexample import find_counterexample, iter_databases
from relalg import (
    INNER, LEFT, NULL, Join, Project, Relation, Scan, Select,
    col, eq, lit, ne, or_,
)


class TestEnumeration(unittest.TestCase):
    def test_increasing_total_size(self):
        schemas = {"R": ("A",), "S": ("B",)}
        totals = [
            sum(len(r.rows) for r in db.values())
            for db in iter_databases(schemas, domain=(0, NULL), max_rows=1)
        ]
        self.assertEqual(totals, sorted(totals))
        self.assertEqual(totals[0], 0)

    def test_multisets_generated_with_repetition(self):
        schemas = {"R": ("A",)}
        dbs = list(iter_databases(schemas, domain=(7,), max_rows=2))
        rows = [db["R"].rows for db in dbs]
        self.assertIn(((7,), (7,)), rows)  # duplicate rows allowed


class TestFindCounterexample(unittest.TestCase):
    def test_null_three_valued_logic_counterexample(self):
        # sigma_{A=1 OR A<>1}(R) vs R: minimal CE is R = {(NULL)}.
        plan_a = Select(or_(eq(col("A"), lit(1)), ne(col("A"), lit(1))),
                        Scan("R"))
        plan_b = Scan("R")
        result = find_counterexample(plan_a, plan_b, {"R": ("A",)}, max_rows=1)
        self.assertTrue(result.found)
        self.assertEqual(result.db["R"].rows, ((NULL,),))
        self.assertEqual(result.output_a.rows, ())
        self.assertEqual(result.output_b.rows, ((NULL,),))

    def test_minimality_by_total_rows(self):
        # inner vs left join: minimal CE has exactly one row overall.
        plan_a = Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=INNER)
        plan_b = Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=LEFT)
        schemas = {"R": ("A", "B"), "S": ("C", "D")}
        result = find_counterexample(plan_a, plan_b, schemas, max_rows=1)
        self.assertTrue(result.found)
        total = sum(len(rel.rows) for rel in result.db.values())
        self.assertEqual(total, 1)

    def test_duplicate_multiplicity_difference_detected(self):
        # pi_A(R LEFT JOIN S) vs pi_A(R): the distinct-value sets are
        # always identical, so any counterexample is purely about
        # duplicate multiplicities.
        plan_a = Project(
            ("A",), Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=LEFT)
        )
        plan_b = Project(("A",), Scan("R"))
        schemas = {"R": ("A", "B"), "S": ("C", "D")}
        result = find_counterexample(plan_a, plan_b, schemas, max_rows=2)
        self.assertTrue(result.found)
        self.assertEqual(
            set(result.output_a.rows), set(result.output_b.rows)
        )
        self.assertNotEqual(
            sorted(result.output_a.rows), sorted(result.output_b.rows)
        )

    def test_equivalent_pair_reports_no_proof(self):
        # Join commutativity: no counterexample; result must not claim proof.
        plan_a = Join(Scan("R"), Scan("S"), on=(("B", "C"),))
        plan_b = Project(
            ("A", "B", "C", "D"),
            Join(Scan("S"), Scan("R"), on=(("C", "B"),)),
        )
        schemas = {"R": ("A", "B"), "S": ("C", "D")}
        result = find_counterexample(plan_a, plan_b, schemas, max_rows=1)
        self.assertFalse(result.found)
        self.assertTrue(result.completed)
        self.assertFalse(result.proves_equivalence)

    def test_schema_mismatch_raises(self):
        plan_a = Project(("A",), Scan("R"))
        plan_b = Scan("R")
        with self.assertRaises(ValueError):
            find_counterexample(plan_a, plan_b, {"R": ("A", "B")})

    def test_max_dbs_budget_cutoff(self):
        plan_a = Scan("R")
        plan_b = Select(eq(col("A"), lit(0)), Scan("R"))
        result = find_counterexample(
            plan_a, plan_b, {"R": ("A",)}, max_rows=5, max_dbs=1
        )
        self.assertFalse(result.found)
        self.assertFalse(result.completed)
        self.assertLessEqual(result.checked, 1)


if __name__ == "__main__":
    unittest.main()
