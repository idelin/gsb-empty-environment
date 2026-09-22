from __future__ import annotations

import unittest

from relational import (
    Aggregate,
    AggExpr,
    ColRef,
    Column,
    Comparison,
    IsNull,
    Join,
    Limit,
    Literal,
    OrderTerm,
    Scan,
    Sort,
    optimize_query,
    relation_multiset,
    sample_tables,
)
from relational.expressions import And, Or
from relational.operators import Filter, LocalTopK, MergeTopK
from relational.planner import PartitionScan


class TopKTests(unittest.TestCase):
    def setUp(self):
        self.tables = sample_tables()
        self.score = Column("scores", "score")
        self.identifier = Column("scores", "id")
        self.order = (OrderTerm(self.score, descending=True), OrderTerm(self.identifier))
        self.original = Limit(Sort(Scan("scores"), self.order), 5, 3)

    def test_local_uses_offset_plus_limit_and_global_merges_stable_ties(self):
        plan = optimize_query(self.original, self.tables)
        self.assertIsInstance(plan.rewritten, MergeTopK)
        self.assertEqual(len(plan.rewritten.children), 2)
        for child in plan.rewritten.children:
            self.assertIsInstance(child, LocalTopK)
            self.assertEqual(child.offset, 0)
            self.assertEqual(child.limit, 8)
        self.assertEqual(plan.rewritten.offset, 5)
        self.assertEqual(plan.rewritten.limit, 3)

        expected_ids = (5, 6, 7)
        self.assertEqual(tuple(row[self.identifier] for row in plan.rewritten.execute(self.tables).rows), expected_ids)
        self.assertEqual(
            relation_multiset(plan.original.execute(self.tables)),
            relation_multiset(plan.rewritten.execute(self.tables)),
        )
        self.assertTrue(plan.proofs[0].safe)
        self.assertIn("offset+limit", plan.proofs[0].reason + "".join(plan.proofs[0].premises))

    def test_limit_only_three_locally_is_wrong(self):
        table = self.tables["scores"]
        wrong_children = tuple(
            LocalTopK(
                partition_id,
                PartitionScan("scores", partition_id, table.schema),
                self.order,
                0,
                3,
            )
            for partition_id in range(2)
        )
        wrong = MergeTopK(wrong_children, self.order, 5, 3)
        wrong_ids = tuple(row[self.identifier] for row in wrong.execute(self.tables).rows)
        correct_ids = tuple(row[self.identifier] for row in self.original.execute(self.tables).rows)
        self.assertNotEqual(wrong_ids, correct_ids)
        self.assertEqual(correct_ids, (5, 6, 7))

    def test_ties_keep_partition_and_local_original_order(self):
        tie_order = (OrderTerm(self.score, descending=True),)
        query = Limit(Sort(Scan("scores"), tie_order), 0, 12)
        plan = optimize_query(query, self.tables)
        ordered_ids = tuple(row[self.identifier] for row in plan.rewritten.execute(self.tables).rows)
        self.assertEqual(ordered_ids, tuple(range(0, 10)) + (10, 11))

    def test_offset_without_limit_is_retained(self):
        query = Limit(Sort(Scan("scores"), self.order), 5, None)
        plan = optimize_query(query, self.tables)
        self.assertFalse(plan.changed)
        self.assertIsInstance(plan.rewritten, Limit)
        self.assertFalse(plan.proofs[0].safe)


class NullAndOuterJoinTests(unittest.TestCase):
    def setUp(self):
        self.tables = sample_tables()
        self.department_id_key = Column("departments", "id")
        self.employee_department_id = Column("employees", "department_id")
        self.salary = Column("employees", "salary")
        self.active = Column("departments", "active")
        self.join = Join(
            Scan("departments"),
            Scan("employees"),
            "left",
            (self.department_id_key,),
            (self.employee_department_id,),
            None,
        )

    def test_null_comparison_unknown_and_is_null(self):
        plan = Filter(Scan("employees"), Comparison("=", ColRef(self.salary), Literal(100)))
        self.assertEqual(
            tuple(row[Column("employees", "id")] for row in plan.execute(self.tables).rows),
            (1,),
        )
        null_plan = Filter(Scan("employees"), IsNull(ColRef(self.salary)))
        self.assertEqual(
            tuple(row[Column("employees", "id")] for row in null_plan.execute(self.tables).rows),
            (5,),
        )

    def test_preserved_side_filter_pushes_safely_and_keeps_unmatched_rows(self):
        query = Filter(self.join, Comparison("=", ColRef(self.active), Literal(True)))
        plan = optimize_query(query, self.tables)
        self.assertTrue(plan.changed)
        self.assertIsInstance(plan.rewritten, Join)
        self.assertEqual(plan.rewritten.join_type, "left")
        original_rows = query.execute(self.tables)
        rewritten_rows = plan.rewritten.execute(self.tables)
        self.assertEqual(relation_multiset(original_rows), relation_multiset(rewritten_rows))
        department_ids = [row[self.department_id_key] for row in rewritten_rows.rows]
        self.assertIn(40, department_ids)
        unmatched_salary = [row[self.salary] for row in rewritten_rows.rows if row[self.department_id_key] == 40]
        self.assertEqual(unmatched_salary, [None])
        self.assertEqual(department_ids.count(10), 2)
        self.assertEqual(department_ids.count(30), 3)

    def test_null_supplying_is_null_predicate_is_retained(self):
        query = Filter(self.join, IsNull(ColRef(self.salary)))
        plan = optimize_query(query, self.tables)
        self.assertFalse(plan.changed)
        self.assertIsInstance(plan.rewritten, Filter)
        self.assertFalse(plan.proofs[0].safe)
        self.assertEqual(
            relation_multiset(query.execute(self.tables)),
            relation_multiset(plan.rewritten.execute(self.tables)),
        )

    def test_null_rejecting_right_predicate_collapses_left_to_inner(self):
        query = Filter(self.join, Comparison(">", ColRef(self.salary), Literal(100)))
        plan = optimize_query(query, self.tables)
        self.assertIsInstance(plan.rewritten, Join)
        self.assertEqual(plan.rewritten.join_type, "inner")
        self.assertEqual(
            relation_multiset(query.execute(self.tables)),
            relation_multiset(plan.rewritten.execute(self.tables)),
        )
        self.assertEqual(len(query.execute(self.tables).rows), 3)

    def test_or_with_is_null_is_not_null_rejecting_and_is_retained(self):
        salary_predicate = Or(
            Comparison(">", ColRef(self.salary), Literal(100)),
            IsNull(ColRef(self.salary)),
        )
        plan = optimize_query(Filter(self.join, salary_predicate), self.tables)
        self.assertFalse(plan.changed)
        self.assertFalse(plan.proofs[0].safe)
        self.assertEqual(
            relation_multiset(Filter(self.join, salary_predicate).execute(self.tables)),
            relation_multiset(plan.rewritten.execute(self.tables)),
        )

    def test_conjunctive_one_side_predicates_can_both_push_down(self):
        predicate = And(
            Comparison(">", ColRef(self.salary), Literal(0)),
            Comparison("=", ColRef(self.active), Literal(True)),
        )
        original_query = Filter(self.join, predicate)
        plan = optimize_query(original_query, self.tables)
        self.assertTrue(plan.changed)
        self.assertEqual(plan.rewritten.join_type, "inner")
        self.assertEqual(
            relation_multiset(original_query.execute(self.tables)),
            relation_multiset(plan.rewritten.execute(self.tables)),
        )


class AggregatePushdownTests(unittest.TestCase):
    def setUp(self):
        self.tables = sample_tables()
        self.department_id_key = Column("departments", "id")
        self.employee_department_id = Column("employees", "department_id")
        self.salary = Column("employees", "salary")
        self.commission = Column("employees", "commission")
        self.join = Join(
            Scan("departments"),
            Scan("employees"),
            "left",
            (self.department_id_key,),
            (self.employee_department_id,),
            None,
        )

    def test_safe_aggregate_pushdown_preserves_null_and_outer_semantics(self):
        query = Aggregate(
            self.join,
            (self.department_id_key,),
            (
                AggExpr("count_star", None, Column("result", "rows")),
                AggExpr("count", ColRef(self.salary), Column("result", "salaries")),
                AggExpr("sum", ColRef(self.salary), Column("result", "payroll")),
                AggExpr("avg", ColRef(self.commission), Column("result", "avg_commission")),
            ),
        )
        plan = optimize_query(query, self.tables)
        self.assertTrue(plan.changed)
        self.assertTrue(all(proof.safe for proof in plan.proofs))
        original = relation_multiset(query.execute(self.tables))
        rewritten = relation_multiset(plan.rewritten.execute(self.tables))
        self.assertEqual(original, rewritten)
        result = {
            row[self.department_id_key]: row.values
            for row in plan.rewritten.execute(self.tables).rows
        }
        self.assertEqual(result[20], (20, 1, 1, 50.0, 5.0))
        self.assertEqual(result[10], (10, 2, 2, 300.0, 10.0))
        self.assertEqual(result[30], (30, 3, 2, 600.0, 8 / 3))
        self.assertEqual(result[40], (40, 1, 0, None, None))

    def test_median_is_not_distributive_and_plan_is_retained(self):
        query = Aggregate(
            self.join,
            (self.department_id_key,),
            (AggExpr("median", ColRef(self.salary), Column("result", "m")),),
        )
        plan = optimize_query(query, self.tables)
        self.assertFalse(plan.changed)
        self.assertFalse(plan.proofs[0].safe)
        self.assertIn("not distributive", plan.proofs[0].reason)

    def test_aggregate_on_non_unique_preserved_side_is_retained(self):
        employee_id = Column("employees", "id")
        employee_department_id = Column("employees", "department_id")
        department_id = Column("departments", "id")
        reversed_join = Join(
            Scan("employees"),
            Scan("departments"),
            "left",
            (employee_department_id,),
            (department_id,),
            None,
        )
        query = Aggregate(
            reversed_join,
            (employee_department_id, department_id),
            (AggExpr("count_star", None, Column("result", "rows")),),
        )
        plan = optimize_query(query, self.tables)
        self.assertFalse(plan.changed)
        self.assertTrue(any("not declared unique" in proof.reason for proof in plan.proofs))
        self.assertEqual(
            relation_multiset(query.execute(self.tables)),
            relation_multiset(plan.rewritten.execute(self.tables)),
        )


if __name__ == "__main__":
    unittest.main()
