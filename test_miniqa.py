import unittest

from demo import build_catalog
from miniqa import Column, Comparison, Literal, Not, Or, Param, Select, UnionAll
from miniqa.planner import EmptyPlan, FullScanPlan, IndexSeekPlan, ProjectPlan, RangeCategory


class MiniQATest(unittest.TestCase):
    def setUp(self):
        self.catalog = build_catalog()
        from miniqa import ExecutionEngine

        self.engine = ExecutionEngine(self.catalog)
        self.amount_query = Select(
            table="orders",
            predicate=Comparison(Column("amount"), ">=", Param("min_amount")),
            outputs=(Column("id"), Column("amount")),
        )
        self.status_query = Select(
            table="orders",
            predicate=Comparison(Column("status"), "=", Param("state")),
            outputs=(Column("id"), Column("status")),
        )

    def test_same_type_different_values_use_cached_plan_without_baked_value(self):
        first = self.engine.execute(self.amount_query, {"min_amount": 70})
        first_plan = list(self.engine.cache._entries.values())[0]
        second = self.engine.execute(self.amount_query, {"min_amount": 25})
        second_plan = list(self.engine.cache._entries.values())[0]

        self.assertIs(first_plan, second_plan)
        self.assertIsInstance(first_plan.physical, ProjectPlan)
        self.assertIsInstance(first_plan.physical.source, IndexSeekPlan)
        self.assertNotIn("70", repr(first_plan))
        self.assertEqual(first[(3, 75)], 1)
        self.assertEqual(first[(4, 90)], 1)
        self.assertEqual(first[(5, 100)], 1)
        self.assertEqual(second[(2, 25)], 2)
        self.assertEqual(second[(3, 75)], 1)
        self.assertEqual(self.engine.cache.stats.lookups, 2)
        self.assertEqual(self.engine.cache.stats.hits, 1)
        self.assertEqual(self.engine.cache.stats.builds, 1)

    def test_range_category_changes_cause_separate_plan(self):
        in_range = self.engine.cache.get_or_build(self.amount_query, {"min_amount": 25})
        above_max = self.engine.cache.get_or_build(self.amount_query, {"min_amount": 1000})

        self.assertIsInstance(in_range.physical, ProjectPlan)
        self.assertIsInstance(in_range.physical.source, IndexSeekPlan)
        self.assertIsInstance(above_max.physical, ProjectPlan)
        self.assertIsInstance(above_max.physical.source, FullScanPlan)
        self.assertEqual(in_range.parameter_profiles[0].range_category, RangeCategory.IN_RANGE)
        self.assertEqual(above_max.parameter_profiles[0].range_category, RangeCategory.ABOVE_MAX)

    def test_type_category_changes_cause_separate_plan(self):
        integer_plan = self.engine.cache.get_or_build(self.amount_query, {"min_amount": 25})
        float_plan = self.engine.cache.get_or_build(self.amount_query, {"min_amount": 25.0})

        self.assertIsNot(integer_plan, float_plan)
        self.assertEqual(integer_plan.parameter_profiles[0].type_name, "int")
        self.assertEqual(float_plan.parameter_profiles[0].type_name, "float")
        self.assertEqual(self.engine.cache.stats.builds, 2)

    def test_equality_different_present_values_share_plan(self):
        paid = self.engine.execute(self.status_query, {"state": "PAID"})
        paid_plan = list(self.engine.cache._entries.values())[0]
        open_orders = self.engine.execute(self.status_query, {"state": "OPEN"})
        open_plan = list(self.engine.cache._entries.values())[0]

        self.assertIs(paid_plan, open_plan)
        self.assertEqual(sum(paid.values()), 5)
        self.assertEqual(open_orders, {(3, "OPEN"): 1})
        self.assertEqual(self.engine.cache.stats.builds, 1)
        self.assertEqual(self.engine.cache.stats.hits, 1)

    def test_parameter_on_left_uses_reversed_index_operator(self):
        query = Select(
            table="orders",
            predicate=Comparison(Param("max_amount"), "<", Column("amount")),
            outputs=(Column("id"), Column("amount")),
        )
        prepared = self.engine.cache.get_or_build(query, {"max_amount": 70})

        self.assertIsInstance(prepared.physical, ProjectPlan)
        self.assertIsInstance(prepared.physical.source, IndexSeekPlan)
        self.assertEqual(prepared.physical.source.operator, ">")
        self.assertEqual(set(self.engine.execute(query, {"max_amount": 70})), {(3, 75), (4, 90), (5, 100)})

    def test_type_mismatch_is_unknown_including_nullable_column(self):
        query = Select(table="orders", predicate=Not(Comparison(Column("amount"), "=", Param("amount"))))
        prepared = self.engine.cache.get_or_build(query, {"amount": "10"})

        self.assertIsInstance(prepared.physical, EmptyPlan)
        self.assertEqual(prepared.physical.reason, "static_unknown")
        self.assertEqual(self.engine.execute(query, {"amount": "10"}), {})

    def test_null_parameter_uses_three_valued_logic(self):
        query = Select(
            table="orders",
            predicate=Or(
                Comparison(Column("status"), "=", Param("state")),
                Comparison(Column("id"), "=", Literal(9)),
            ),
        )
        prepared = self.engine.cache.get_or_build(query, {"state": None})

        self.assertIsInstance(prepared.physical, EmptyPlan)
        self.assertEqual(prepared.physical.reason, "static_unknown")
        self.assertEqual(self.engine.execute(query, {"state": None}), {})

        true_branch = Select(
            table="orders",
            predicate=Or(
                Comparison(Column("status"), "=", Param("state")),
                Comparison(Column("id"), "=", Literal(3)),
            ),
        )
        self.assertEqual(set(self.engine.execute(true_branch, {"state": None})), {(3, "OPEN", 75)})

        negated = Select(table="orders", predicate=Not(Comparison(Column("status"), "=", Param("state"))))
        self.assertEqual(self.engine.execute(negated, {"state": None}), {})

    def test_null_column_is_unknown_even_under_negation(self):
        query = Select(table="orders", predicate=Not(Comparison(Column("amount"), "=", Literal(10))))
        result = self.engine.execute(query)
        self.assertNotIn((6, "CANCELLED", None), result)
        self.assertIn((2, "PAID", 25), result)

    def test_multiset_duplicates_and_union_all_are_preserved(self):
        paid = self.engine.execute(self.status_query, {"state": "PAID"})
        self.assertEqual(paid[(2, "PAID")], 2)
        self.assertEqual(sum(paid.values()), 5)

        amount_ids = Select(
            table="orders",
            predicate=Comparison(Column("amount"), ">=", Param("min_amount")),
            outputs=(Column("id"),),
        )
        status_ids = Select(
            table="orders",
            predicate=Comparison(Column("status"), "=", Param("state")),
            outputs=(Column("id"),),
        )
        union = UnionAll(amount_ids, status_ids)
        result = self.engine.execute(union, {"min_amount": 100, "state": "PAID"})
        self.assertEqual(result[(5,)], 2)
        self.assertEqual(result[(2,)], 2)
        self.assertEqual(result[(1,)], 1)
        self.assertEqual(result[(4,)], 1)
        self.assertEqual(sum(result.values()), 6)

    def test_statistics_change_invalidates_and_replans(self):
        self.engine.execute(self.amount_query, {"min_amount": 25})
        self.assertEqual(self.engine.cache.stats.invalidations, 0)

        self.catalog.refresh_stats("orders")
        self.engine.execute(self.amount_query, {"min_amount": 25})

        self.assertEqual(self.engine.cache.stats.invalidations, 1)
        self.assertEqual(self.engine.cache.stats.builds, 2)


if __name__ == "__main__":
    unittest.main()
