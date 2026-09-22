import unittest

from mini_sql import Database, QueryPlanner, Relation, UnsupportedSQL
from mini_sql.runtime import GroupJoin, SemiJoin


def make_database():
    customers = Relation(
        ("id", "name"),
        (
            (1, "Ada"),
            (2, "Bo"),
            (3, "Cy"),
            (2, "Bo"),
        ),
    )
    payments = Relation(
        ("id", "customer_id", "amount"),
        (
            (10, 1, 100),
            (11, 1, 50),
            (12, 2, None),
        ),
    )
    things = Relation(
        ("id", "parent_id", "label"),
        ((2, 1, "inner-id-2-matches-outer-1"),),
    )
    return Database({
        "customers": customers,
        "payments": payments,
        "things": things,
    })


class SubqueryTests(unittest.TestCase):
    def setUp(self):
        self.planner = QueryPlanner(make_database())

    def execute(self, sql):
        return self.planner.execute(sql)

    def test_exists_uses_semijoin_and_does_not_duplicate_outer_rows(self):
        sql = """
            SELECT customers.name
            FROM customers
            WHERE EXISTS (
                SELECT * FROM payments
                WHERE payments.customer_id = customers.id
            )
        """
        plan = self.planner.plan(sql)
        self.assertIsInstance(plan.input_plan, SemiJoin)
        self.assertEqual(self.execute(sql), [("Ada",), ("Bo",), ("Bo",)])

    def test_not_exists_uses_anti_join_for_no_match(self):
        sql = """
            SELECT customers.name
            FROM customers
            WHERE NOT EXISTS (
                SELECT * FROM payments
                WHERE payments.customer_id = customers.id
            )
        """
        plan = self.planner.plan(sql)
        self.assertTrue(plan.input_plan.negated)
        self.assertEqual(self.execute(sql), [("Cy",)])

    def test_scalar_aggregates_group_outer_rows_once_with_null_and_empty_rules(self):
        sql = """
            SELECT
                customers.name,
                (SELECT COUNT(*)
                 FROM payments
                 WHERE payments.customer_id = customers.id) AS c_all,
                (SELECT COUNT(payments.amount)
                 FROM payments
                 WHERE payments.customer_id = customers.id) AS c_value,
                (SELECT SUM(payments.amount)
                 FROM payments
                 WHERE payments.customer_id = customers.id) AS total
            FROM customers
        """
        plan = self.planner.plan(sql)
        self.assertIsInstance(plan.input_plan, GroupJoin)
        self.assertEqual(
            self.execute(sql),
            [
                ("Ada", 2, 2, 150),
                ("Bo", 1, 0, None),
                ("Cy", 0, 0, None),
                ("Bo", 1, 0, None),
            ],
        )

    def test_correlated_unqualified_column_binds_to_nearest_scope(self):
        sql = """
            SELECT customers.name
            FROM customers
            WHERE EXISTS (
                SELECT * FROM things
                WHERE things.parent_id = customers.id
                  AND id = 2
            )
        """
        self.assertEqual(self.execute(sql), [("Ada",)])

    def test_correlated_qualified_outer_column_still_binds_outer_scope(self):
        sql = """
            SELECT customers.name
            FROM customers
            WHERE EXISTS (
                SELECT * FROM things
                WHERE things.parent_id = customers.id
                  AND things.id <> customers.id
            )
        """
        self.assertEqual(self.execute(sql), [("Ada",)])

    def test_three_valued_null_predicate_does_not_match(self):
        sql = """
            SELECT customers.name
            FROM customers
            WHERE EXISTS (
                SELECT * FROM payments
                WHERE payments.customer_id = customers.id
                  AND payments.amount = 50
            )
        """
        self.assertEqual(self.execute(sql), [("Ada",)])

    def test_inner_only_and_outer_only_subquery_predicates_are_not_join_conditions(self):
        database = Database({
            "parents": Relation(("id", "active"), ((1, True), (2, False))),
            "children": Relation(("id", "parent_id", "ready"), ((1, 1, True), (2, 1, False))),
        })
        planner = QueryPlanner(database)
        sql = """
            SELECT parents.id
            FROM parents
            WHERE EXISTS (
                SELECT * FROM children
                WHERE children.ready = TRUE
                  AND parents.active = TRUE
            )
        """
        self.assertEqual(planner.execute(sql), [(1,)])

    def test_outer_filter_applies_before_subquery_and_duplicates_are_retained(self):
        sql = """
            SELECT
                customers.name,
                (SELECT COUNT(*) FROM payments WHERE payments.customer_id = customers.id)
            FROM customers
            WHERE customers.id = 2
        """
        self.assertEqual(self.execute(sql), [("Bo", 1), ("Bo", 1)])

    def test_unquoted_identifiers_are_case_insensitive_and_relation_column_names_are_preserved(self):
        database = Database({
            "Customers": Relation(("CustomerID", "Name"), ((1, "Ada"), (2, "Bo"))),
        })
        planner = QueryPlanner(database)
        plan = planner.plan("SELECT customers.* FROM customers")
        self.assertEqual(plan.schema, ("CustomerID", "Name"))
        self.assertEqual(
            planner.execute(
                "SELECT customers.NAME FROM customers WHERE customers.customerid = 1"
            ),
            [("Ada",)],
        )


class RejectedSyntaxTests(unittest.TestCase):
    def setUp(self):
        self.planner = QueryPlanner(make_database())

    def assert_unsupported(self, sql):
        with self.assertRaises(UnsupportedSQL):
            self.planner.plan(sql)

    def test_join_is_rejected(self):
        self.assert_unsupported(
            "SELECT c.name FROM customers c JOIN payments p ON p.customer_id = c.id"
        )

    def test_in_is_rejected(self):
        self.assert_unsupported("SELECT name FROM customers WHERE id IN (1, 2)")

    def test_distinct_is_rejected(self):
        self.assert_unsupported("SELECT DISTINCT name FROM customers")

    def test_group_by_is_rejected(self):
        self.assert_unsupported(
            "SELECT customer_id, SUM(amount) FROM payments GROUP BY customer_id"
        )

    def test_non_aggregate_scalar_subquery_is_rejected(self):
        self.assert_unsupported(
            """
            SELECT name, (SELECT payments.amount FROM payments
                          WHERE payments.customer_id = customers.id)
            FROM customers
            """
        )

    def test_nested_subquery_is_rejected(self):
        self.assert_unsupported(
            """
            SELECT c.name FROM customers c
            WHERE EXISTS (
                SELECT * FROM payments p
                WHERE p.customer_id = c.id
                  AND EXISTS (SELECT * FROM customers c2 WHERE c2.id = c.id)
            )
            """
        )

    def test_exists_inside_or_is_rejected(self):
        self.assert_unsupported(
            """
            SELECT name FROM customers
            WHERE id = 1 OR EXISTS (
                SELECT * FROM payments WHERE payments.customer_id = customers.id
            )
            """
        )


if __name__ == "__main__":
    unittest.main()
