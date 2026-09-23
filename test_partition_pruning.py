from __future__ import annotations

import unittest
from itertools import product

from partition_pruning import (
    F,
    T,
    U,
    Cmp,
    Col,
    ColumnSpec,
    EnumFragment,
    IsNull,
    Lit,
    Not,
    NullFragment,
    NullDomain,
    Param,
    PartitionScheme,
    RangeFragment,
    RelationTable,
    and_,
    concrete_eval,
    in_,
    not_in,
    or_,
    prune,
    select_rows,
)


def integer_scheme() -> PartitionScheme:
    return PartitionScheme.build(
        [ColumnSpec("x", "integer")],
        {
            "x": (
                RangeFragment("negative", None, -1),
                RangeFragment("zero_ten_open", 0, 10, upper_inclusive=False),
                RangeFragment("ten_to_twenty", 10, 20),
                RangeFragment("over_twenty", 21, None),
                NullFragment("x_null"),
            )
        },
    )


def enum_scheme() -> PartitionScheme:
    return PartitionScheme.build(
        [ColumnSpec("g", "enum")],
        {
            "g": (
                EnumFragment("a_b", frozenset({"a", "b"})),
                EnumFragment("c", frozenset({"c"})),
                NullFragment("g_null"),
            )
        },
    )


def composite_scheme() -> PartitionScheme:
    return PartitionScheme.build(
        [ColumnSpec("x", "integer"), ColumnSpec("g", "enum")],
        {
            "x": (
                RangeFragment("low", None, 0),
                RangeFragment("high", 1, None),
                NullFragment("x_null"),
            ),
            "g": (
                EnumFragment("a", frozenset({"a"})),
                EnumFragment("b", frozenset({"b"})),
                NullFragment("g_null"),
            ),
        },
    )


class RangeTests(unittest.TestCase):
    def test_open_upper_bound_is_canonical_closed_integer_range(self) -> None:
        fragment = RangeFragment("zero_ten", 0, 10, upper_inclusive=False)
        self.assertEqual((fragment.lower, fragment.upper), (0, 9))

    def test_open_lower_bound_is_canonical_closed_integer_range(self) -> None:
        fragment = RangeFragment("ten", 10, 20, lower_inclusive=False)
        self.assertEqual((fragment.lower, fragment.upper), (11, 20))

    def test_gap_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            PartitionScheme.build(
                [ColumnSpec("x", "integer")],
                {"x": (RangeFragment("a", None, 0), RangeFragment("b", 2, None))},
            )

    def test_overlap_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            PartitionScheme.build(
                [ColumnSpec("x", "integer")],
                {"x": (RangeFragment("a", None, 5), RangeFragment("b", 5, None))},
            )

    def test_boundary_rows_select_exact_partitions(self) -> None:
        scheme = integer_scheme()
        table = RelationTable("t", scheme)
        for value in (9, 10, 20, 21, None):
            table.insert({"x": value})
        plan = prune(Cmp("=", Col("x"), Lit(10)), scheme)
        self.assertEqual([p.id for p in plan.selected], ["ten_to_twenty"])
        self.assertEqual(select_rows(table, Cmp("=", Col("x"), Lit(9)), columns=("x",)), [(9,)])
        self.assertEqual(select_rows(table, Cmp(">=", Col("x"), Lit(21)), columns=("x",)), [(21,)])

    def test_null_range_partition_is_independent(self) -> None:
        scheme = integer_scheme()
        plan = prune(Cmp("=", Col("x"), Lit(10)), scheme)
        self.assertNotIn("x_null", [p.id for p in plan.selected])
        null_plan = prune(IsNull(Col("x")), scheme)
        self.assertEqual([p.id for p in null_plan.selected], ["x_null"])


class EnumTests(unittest.TestCase):
    def test_enum_equality_and_in_list(self) -> None:
        scheme = enum_scheme()
        self.assertEqual(
            [p.id for p in prune(Cmp("=", Col("g"), Lit("c")), scheme).selected],
            ["c"],
        )
        self.assertEqual(
            [p.id for p in prune(in_(Col("g"), [Lit("a"), Lit("b")]), scheme).selected],
            ["a_b"],
        )
        not_plan = prune(not_in(Col("g"), [Lit("a")]), scheme)
        self.assertEqual([p.id for p in not_plan.selected], ["a_b", "c"])
        self.assertNotIn("g_null", [p.id for p in not_plan.selected])

    def test_in_list_with_null_keeps_null_partition_unknown_only(self) -> None:
        scheme = enum_scheme()
        plan = prune(in_(Col("g"), [Lit("a"), Lit(None)]), scheme)
        self.assertEqual([p.id for p in plan.selected], ["a_b"])
        skipped = {info.partition.id: info.state for info in plan.skipped}
        self.assertEqual(skipped["c"], frozenset({F, U}))
        self.assertEqual(skipped["g_null"], frozenset({U}))

    def test_overlapping_enum_partition_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            PartitionScheme.build(
                [ColumnSpec("g", "enum")],
                {"g": (EnumFragment("a", frozenset({"a"})), EnumFragment("a2", frozenset({"a", "b"})))},
            )


class ThreeValuedLogicTests(unittest.TestCase):
    def test_comparison_and_is_null_semantics(self) -> None:
        self.assertEqual(concrete_eval(Cmp("=", Col("x"), Lit(1)), {"x": None}), U)
        self.assertEqual(concrete_eval(IsNull(Col("x")), {"x": None}), T)
        self.assertEqual(concrete_eval(Not(IsNull(Col("x"))), {"x": 1}), T)
        self.assertEqual(
            concrete_eval(and_(Cmp("=", Col("x"), Lit(1)), IsNull(Col("x"))), {"x": None}),
            U,
        )
        self.assertEqual(
            concrete_eval(or_(Cmp("=", Col("x"), Lit(1)), IsNull(Col("x"))), {"x": None}),
            T,
        )


    def test_type_mismatch_is_unknown_and_does_not_select_partition(self) -> None:
        scheme = integer_scheme()
        plan = prune(Cmp("=", Col("x"), Lit("not-an-int")), scheme)
        self.assertEqual(plan.selected, ())
        for skip in plan.skipped:
            self.assertNotIn(T, skip.state)

    def test_in_list_three_valued_logic(self) -> None:
        self.assertEqual(concrete_eval(in_(Col("g"), [Lit("a"), Lit(None)]), {"g": "b"}), U)
        self.assertEqual(concrete_eval(in_(Col("g"), [Lit("a"), Lit(None)]), {"g": "a"}), T)
        self.assertEqual(concrete_eval(not_in(Col("g"), [Lit("a"), Lit(None)]), {"g": "b"}), U)


class CompositeAndOrTests(unittest.TestCase):
    def test_or_uses_union_across_partitions(self) -> None:
        scheme = composite_scheme()
        predicate = or_(Cmp("<=", Col("x"), Lit(0)), Cmp("=", Col("g"), Lit("b")))
        selected = {p.id for p in prune(predicate, scheme).selected}
        self.assertIn("low|a", selected)
        self.assertIn("high|b", selected)
        self.assertNotIn("high|a", selected)
        self.assertIn("x_null|b", selected)

    def test_and_intersects_partition_conditions(self) -> None:
        scheme = composite_scheme()
        predicate = and_(Cmp(">", Col("x"), Lit(0)), Cmp("=", Col("g"), Lit("a")))
        self.assertEqual([p.id for p in prune(predicate, scheme).selected], ["high|a"])

    def test_each_null_partition_is_separately_represented(self) -> None:
        scheme = composite_scheme()
        ids = {partition.id for partition in scheme.partitions}
        self.assertEqual(len(ids), 9)
        self.assertIn("x_null|g_null", ids)
        plan = prune(and_(IsNull(Col("x")), Not(IsNull(Col("g")))), scheme)
        self.assertEqual(
            [p.id for p in plan.selected],
            ["x_null|a", "x_null|b"],
        )


class ParameterAndExecutorTests(unittest.TestCase):
    def test_unbound_parameter_conservatively_keeps_all_partitions(self) -> None:
        scheme = composite_scheme()
        predicate = and_(Cmp(">=", Col("x"), Param("lo")), Cmp("=", Col("g"), Param("g")))
        plan = prune(predicate, scheme, {})
        self.assertEqual(len(plan.selected), len(scheme.partitions))
        self.assertEqual(plan.skipped, ())

    def test_bound_parameter_prunes_and_null_parameter_skips_nonnull_fragments(self) -> None:
        scheme = composite_scheme()
        predicate = and_(Cmp(">=", Col("x"), Param("lo")), Cmp("=", Col("g"), Param("g")))
        plan = prune(predicate, scheme, {"lo": 2, "g": "b"})
        self.assertEqual([p.id for p in plan.selected], ["high|b"])
        null_param = prune(Cmp("=", Col("g"), Param("g")), scheme, {"g": None})
        self.assertEqual([p.id for p in null_param.selected], [])
        self.assertIn("x_null|g_null", [s.partition.id for s in null_param.skipped])

    def test_multiset_preserves_duplicates_and_order(self) -> None:
        scheme = enum_scheme()
        table = RelationTable("t", scheme)
        for group in ("a", "a", "b", "c"):
            table.insert({"g": group})
        rows = select_rows(
            table,
            in_(Col("g"), [Lit("a"), Lit("b")]),
            columns=("g",),
        )
        self.assertEqual(rows, [("a",), ("a",), ("b",)])

    def test_non_partition_column_does_not_permit_pruning(self) -> None:
        scheme = enum_scheme()
        plan = prune(Cmp("=", Col("other"), Lit(1)), scheme)
        self.assertEqual(len(plan.selected), len(scheme.partitions))
        table = RelationTable("t", scheme)
        table.insert({"g": "a", "other": 1})
        self.assertEqual(
            select_rows(table, Cmp("=", Col("other"), Lit(1)), columns=("other",)),
            [(1,)],
        )


class SoundnessTests(unittest.TestCase):
    def test_pruned_scan_equals_brute_force_scan_for_compound_predicates(self) -> None:
        scheme = composite_scheme()
        table = RelationTable("t", scheme)
        x_values = (-1, 0, 1, None)
        g_values = ("a", "b", None)
        for x, group in product(x_values, g_values):
            table.insert({"x": x, "g": group})
        predicates = [
            or_(Cmp("<=", Col("x"), Lit(0)), Cmp("=", Col("g"), Lit("b"))),
            and_(Cmp(">=", Col("x"), Lit(1)), in_(Col("g"), [Lit("a"), Lit(None)])),
            or_(
                and_(Cmp("<", Col("x"), Lit(0)), Cmp("=", Col("g"), Lit("a"))),
                IsNull(Col("x")),
            ),
            Not(or_(Cmp("=", Col("x"), Lit(0)), IsNull(Col("g")))),
        ]
        for predicate in predicates:
            expected = [
                (row["x"], row["g"])
                for row in table.rows
                if concrete_eval(predicate, row) == T
            ]
            self.assertEqual(
                select_rows(table, predicate, columns=("x", "g")),
                expected,
                predicate,
            )
            plan = prune(predicate, scheme)
            for row in expected:
                self.assertIn(
                    scheme.route({"x": row[0], "g": row[1]}).id,
                    [p.id for p in plan.selected],
                )

    def test_skip_proof_root_state_never_contains_true(self) -> None:
        scheme = integer_scheme()
        plan = prune(Cmp(">", Col("x"), Lit(100)), scheme)
        for skip in plan.skipped:
            self.assertNotIn(T, skip.state)
            self.assertTrue(skip.proof.reason)


if __name__ == "__main__":
    unittest.main(verbosity=2)
