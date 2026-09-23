import pathlib
import unittest

from miniplanner import (
    Catalog,
    ColumnStats,
    Query,
    QueryPlanner,
    Table,
    and_,
    col,
    eq,
    ge,
    gt,
    is_not_null,
    is_null,
    le,
    lit,
    lt,
    ne,
    not_,
    or_,
    param,
)


ROOT = pathlib.Path(__file__).resolve().parents[1]


def make_planner():
    people = Table(
        "people",
        [
            {"id": 1, "name": "Ada", "age": 10, "dept": "eng"},
            {"id": 2, "name": "Ada", "age": 10, "dept": "eng"},
            {"id": 3, "name": "Cy", "age": 30, "dept": "sales"},
            {"id": 4, "name": "Bo", "age": 40, "dept": None},
            {"id": 5, "name": "Nia", "age": None, "dept": "sales"},
        ],
    )
    catalog = Catalog([people])
    return QueryPlanner(catalog), catalog, people


class PlannerTests(unittest.TestCase):
    def test_same_type_same_category_reuses_cached_parameterized_plan(self):
        planner, _, _ = make_planner()
        query = Query("people", eq(col("name"), param(0)), ("id", "name"))
        prepared = planner.prepare(query)

        first = prepared.execute(["Ada"])
        second = prepared.execute(["Cy"])

        self.assertFalse(first.cache_hit)
        self.assertTrue(second.cache_hit)
        self.assertEqual(first.plan_name, second.plan_name)
        self.assertEqual([1, 2], [row["id"] for row in first.rows])
        self.assertEqual([3], [row["id"] for row in second.rows])
        self.assertEqual(1, planner.cache.stats.hits)
        self.assertEqual(1, planner.cache.stats.replans)

    def test_planned_index_predicate_still_contains_parameter_not_first_value(self):
        planner, _, _ = make_planner()
        prepared = planner.prepare(Query("people", eq(col("name"), param(0))))
        first_plan = prepared.plan_for(["Ada"])
        cached_plan = prepared.plan_for(["Cy"])

        self.assertIs(first_plan, cached_plan)
        self.assertIsNotNone(cached_plan.access)
        self.assertEqual(param(0), cached_plan.access.scalar)
        self.assertEqual("Ada", first_plan.access.value(["Ada"]))
        self.assertEqual("Cy", cached_plan.access.value(["Cy"]))

    def test_same_type_different_range_category_causes_new_plan(self):
        planner, _, _ = make_planner()
        prepared = planner.prepare(Query("people", lt(col("age"), param(0)), ("id",)))

        low = prepared.execute([15])
        high = prepared.execute([35])

        self.assertEqual("INDEX_RANGE_SCAN", low.plan_name)
        self.assertEqual("FULL_SCAN", high.plan_name)
        self.assertFalse(low.cache_hit)
        self.assertFalse(high.cache_hit)
        self.assertEqual([1, 2], [row["id"] for row in low.rows])
        self.assertEqual([1, 2, 3], [row["id"] for row in high.rows])

    def test_null_comparison_is_unknown_and_filters_every_row(self):
        planner, _, _ = make_planner()
        query = Query("people", lt(col("age"), param(0)), ("id",))
        prepared = planner.prepare(query)

        result = prepared.execute([None])

        self.assertEqual("EMPTY_RESULT", result.plan_name)
        self.assertEqual([], result.rows)
        self.assertEqual("unknown", planner.plan_for(query, [None]).simplification)

    def test_three_valued_logic_for_or_not_and_null_predicates(self):
        planner, _, _ = make_planner()

        unknown_or_true = planner.prepare(
            Query("people", or_(eq(col("age"), param(0)), eq(col("age"), lit(30))), ("id",))
        ).execute([None])
        unknown_or_false = planner.prepare(
            Query("people", or_(eq(col("age"), param(0)), eq(col("age"), lit(99))), ("id",))
        ).execute([None])
        not_null = planner.prepare(
            Query("people", not_(lt(col("age"), param(0))), ("id",))
        ).execute([None])
        null_rows = planner.prepare(Query("people", is_null(col("age")), ("id",))).execute([])
        non_null_rows = planner.prepare(Query("people", is_not_null(col("age")), ("id",))).execute([])

        self.assertEqual([3], [row["id"] for row in unknown_or_true.rows])
        self.assertEqual([], unknown_or_false.rows)
        self.assertEqual([], not_null.rows)
        self.assertEqual([5], [row["id"] for row in null_rows.rows])
        self.assertEqual([1, 2, 3, 4], [row["id"] for row in non_null_rows.rows])

    def test_multiset_rows_and_projection_are_preserved(self):
        planner, _, _ = make_planner()
        result = planner.prepare(
            Query("people", and_(eq(col("name"), param(0)), eq(col("age"), lit(10))), ("id", "name"))
        ).execute(["Ada"])

        self.assertEqual(
            [{"id": 1, "name": "Ada"}, {"id": 2, "name": "Ada"}],
            result.rows,
        )

    def test_incompatible_types_have_separate_cache_entries_and_static_semantics(self):
        planner, _, _ = make_planner()
        equality = planner.prepare(Query("people", eq(col("age"), param(0)), ("id",)))
        inequality = planner.prepare(Query("people", ne(col("age"), param(0)), ("id",)))

        number = equality.execute([30])
        text = equality.execute(["30"])
        not_equal_text = inequality.execute(["30"])

        self.assertEqual([3], [row["id"] for row in number.rows])
        self.assertEqual([], text.rows)
        self.assertEqual([1, 2, 3, 4], [row["id"] for row in not_equal_text.rows])
        self.assertEqual(3, planner.cache.stats.misses)

    def test_stats_change_invalidates_cache_and_triggers_replan(self):
        planner, catalog, table = make_planner()
        prepared = planner.prepare(Query("people", lt(col("age"), param(0)), ("id",)))
        before = prepared.execute([35])
        self.assertEqual("FULL_SCAN", before.plan_name)

        catalog.replace_table(
            table.with_stats(
                {
                    "name": table.column_stats("name"),
                    "age": ColumnStats(0, 100, 1, 4, "int"),
                    "dept": table.column_stats("dept"),
                }
            )
        )
        after = prepared.execute([35])

        self.assertEqual(1, planner.cache.stats.invalidations)
        self.assertFalse(after.cache_hit)
        self.assertEqual("INDEX_RANGE_SCAN", after.plan_name)
        self.assertEqual([1, 2, 3], [row["id"] for row in after.rows])
        self.assertGreater(after.catalog_version, before.catalog_version)

    def test_out_of_range_statistics_allow_empty_result(self):
        planner, _, _ = make_planner()
        result = planner.prepare(Query("people", gt(col("age"), param(0)), ("id",))).execute([1000])
        self.assertEqual("EMPTY_RESULT", result.plan_name)
        self.assertEqual([], result.rows)

    def test_cache_key_has_type_and_range_category_but_no_values(self):
        planner, _, _ = make_planner()
        prepared = planner.prepare(Query("people", lt(col("age"), param(0))))
        prepared.execute([15])
        keys = list(planner.cache.entries)

        self.assertEqual(1, len(keys))
        key = keys[0]
        profile = key[2]
        comparison_profile = profile[1][1][0]
        self.assertEqual(("int", "within_low"), comparison_profile[-2:])
        self.assertNotIn(15, comparison_profile)

    def test_parameter_type_is_part_of_cache_key_without_comparison_range(self):
        planner, _, _ = make_planner()
        prepared = planner.prepare(Query("people", is_null(param(0)), ("id",)))

        null_result = prepared.execute([None])
        number_result = prepared.execute([42])

        self.assertEqual([1, 2, 3, 4, 5], [row["id"] for row in null_result.rows])
        self.assertEqual([], number_result.rows)
        self.assertFalse(number_result.cache_hit)

    def test_not_null_comparison_is_unknown_and_mixed_type_index_is_safe(self):
        table = Table(
            "mixed",
            [
                {"id": 1, "value": 10},
                {"id": 2, "value": "10"},
                {"id": 3, "value": None},
            ],
        )
        planner = QueryPlanner(Catalog([table]))
        not_null = planner.prepare(
            Query("mixed", not_(lt(col("value"), param(0))), ("id",))
        ).execute([None])
        equality = planner.prepare(
            Query("mixed", eq(col("value"), param(0)), ("id",))
        ).execute(["10"])

        self.assertEqual("EMPTY_RESULT", not_null.plan_name)
        self.assertEqual([], not_null.rows)
        self.assertEqual([2], [row["id"] for row in equality.rows])

    def test_ordered_operators_and_boundaries(self):
        planner, _, _ = make_planner()
        cases = [
            (lt, 30, [1, 2]),
            (le, 30, [1, 2, 3]),
            (gt, 30, [4]),
            (ge, 30, [3, 4]),
        ]
        for operator, value, expected in cases:
            with self.subTest(operator=operator.__name__, value=value):
                result = planner.prepare(
                    Query("people", operator(col("age"), param(0)), ("id",))
                ).execute([value])
                self.assertEqual(expected, [row["id"] for row in result.rows])

    def test_no_sql_engine_module_is_imported_by_implementation(self):
        source_files = list((ROOT / "miniplanner").glob("*.py")) + [ROOT / "run_sample.py"]
        for source_file in source_files:
            with self.subTest(file=source_file.name):
                source = source_file.read_text(encoding="utf-8")
                self.assertNotIn("sqlite3", source)
                self.assertNotIn("sqlalchemy", source)


if __name__ == "__main__":
    unittest.main()
