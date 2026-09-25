import math
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from block_pruning import analyze_block, block_outcomes
from predicate_tree import (
    And,
    Block,
    Column,
    ColumnStats,
    ColumnType,
    Comparison,
    ComparisonOp as Op,
    IsNotNull,
    IsNull,
    Literal,
    Not,
    Or,
    PredicateError,
    TriBool,
    evaluate_row,
    not3,
    typecheck_predicate,
)
from query import (
    audit_pruning,
    scan_row_by_row,
    scan_with_pruning,
    typed_batch,
)


X_INT = Column("x", ColumnType.INTEGER)
X_FLOAT = Column("x", ColumnType.FLOAT)
FLAG = Column("flag", ColumnType.BOOLEAN)


class ThreeValuedLogicTests(unittest.TestCase):
    def test_not_unknown_remains_unknown(self):
        self.assertIs(not3(TriBool.UNKNOWN), TriBool.UNKNOWN)
        row = {"x": None}
        self.assertIs(evaluate_row(Not(Comparison(X_INT, Op.GT, Literal(2))), row), TriBool.UNKNOWN)

    def test_null_is_distinct_from_nan(self):
        self.assertIs(evaluate_row(IsNull(X_FLOAT), {"x": None}), TriBool.TRUE)
        self.assertIs(evaluate_row(IsNull(X_FLOAT), {"x": math.nan}), TriBool.FALSE)
        self.assertIs(evaluate_row(IsNotNull(X_FLOAT), {"x": math.nan}), TriBool.TRUE)

    def test_nan_comparison_semantics(self):
        predicate = Comparison(X_FLOAT, Op.GT, Literal(2.0, ColumnType.FLOAT))
        self.assertIs(evaluate_row(predicate, {"x": math.nan}), TriBool.FALSE)
        self.assertIs(evaluate_row(Not(predicate), {"x": math.nan}), TriBool.TRUE)
        different = Comparison(X_FLOAT, Op.NE, Literal(math.nan, ColumnType.FLOAT))
        self.assertIs(evaluate_row(different, {"x": math.nan}), TriBool.TRUE)

    def test_large_integers_are_exact(self):
        big = 10**40 + 1
        predicate = Comparison(X_INT, Op.EQ, Literal(big))
        self.assertIs(evaluate_row(predicate, {"x": big}), TriBool.TRUE)
        self.assertIs(evaluate_row(predicate, {"x": big + 1}), TriBool.FALSE)

    def test_typed_literals_reject_float_for_integer_column(self):
        with self.assertRaises(PredicateError):
            evaluate_row(Comparison(X_INT, Op.EQ, Literal(2.0)), {"x": 2})


class BlockPruningTests(unittest.TestCase):
    def block(self, rows):
        return typed_batch([X_FLOAT], rows).block

    def test_all_null_block_skips_comparison_but_not_is_null(self):
        rows = [{"x": None}, {"x": None}]
        block = self.block(rows)
        comparison = Comparison(X_FLOAT, Op.GT, Literal(2.0, ColumnType.FLOAT))
        decision = analyze_block(comparison, block)
        self.assertTrue(decision.skip)
        self.assertEqual(decision.possible_outcomes, frozenset({TriBool.UNKNOWN}))
        self.assertIn("全为 NULL", decision.reason)

        null_check = analyze_block(IsNull(X_FLOAT), block)
        self.assertFalse(null_check.skip)
        self.assertEqual(null_check.possible_outcomes, frozenset({TriBool.TRUE}))

    def test_nan_stats_separate_value_range_and_negated_predicate(self):
        rows = [{"x": 1.0}, {"x": math.nan}]
        block = self.block(rows)
        stats = block.stats[X_FLOAT]
        self.assertEqual(stats.nan_count, 1)
        self.assertEqual(stats.value_count, 1)
        self.assertEqual((stats.minimum, stats.maximum), (1.0, 1.0))

        greater = Comparison(X_FLOAT, Op.GT, Literal(2.0, ColumnType.FLOAT))
        self.assertEqual(
            block_outcomes(greater, block),
            frozenset({TriBool.FALSE}),
        )
        self.assertEqual(
            block_outcomes(Not(greater), block),
            frozenset({TriBool.TRUE}),
        )

    def test_nan_keeps_not_equal_block_from_being_skipped(self):
        rows = [{"x": 2.0}, {"x": math.nan}]
        block = self.block(rows)
        predicate = Comparison(X_FLOAT, Op.NE, Literal(2.0, ColumnType.FLOAT))
        decision = analyze_block(predicate, block)
        self.assertFalse(decision.skip)
        self.assertEqual(
            decision.possible_outcomes,
            frozenset({TriBool.TRUE, TriBool.FALSE}),
        )

    def test_integer_bounds_prove_range_absence_without_float(self):
        stats = ColumnStats(null_count=0, value_count=3, minimum=7, maximum=9)
        block = Block(3, {X_INT: stats})
        predicate = Comparison(X_INT, Op.GT, Literal(10))
        decision = analyze_block(predicate, block)
        self.assertTrue(decision.skip)
        self.assertEqual(decision.possible_outcomes, frozenset({TriBool.FALSE}))
        self.assertIn("最大值 9 <= 10", decision.reason)

    def test_eq_range_cannot_prove_absence_without_value_dictionary(self):
        stats = ColumnStats(null_count=0, value_count=2, minimum=1, maximum=3)
        block = Block(2, {X_INT: stats})
        predicate = Comparison(X_INT, Op.EQ, Literal(2))
        decision = analyze_block(predicate, block)
        self.assertFalse(decision.skip)
        self.assertEqual(
            decision.possible_outcomes,
            frozenset({TriBool.TRUE, TriBool.FALSE}),
        )

    def test_singleton_ne_can_be_proved_absent(self):
        stats = ColumnStats(null_count=0, value_count=2, minimum=2, maximum=2)
        block = Block(2, {X_INT: stats})
        predicate = Comparison(X_INT, Op.NE, Literal(2))
        decision = analyze_block(predicate, block)
        self.assertTrue(decision.skip)
        self.assertEqual(decision.possible_outcomes, frozenset({TriBool.FALSE}))

    def test_ne_literal_without_matching_value_stays_scannable(self):
        stats = ColumnStats(null_count=0, value_count=2, minimum=2, maximum=2)
        block = Block(2, {X_INT: stats})
        predicate = Comparison(X_INT, Op.NE, Literal(3))
        decision = analyze_block(predicate, block)
        self.assertFalse(decision.skip)

    def test_malformed_predicate_requires_literal(self):
        with self.assertRaises(PredicateError):
            typecheck_predicate(Comparison(X_INT, Op.EQ, 2))

    def test_typed_batch_rejects_extra_columns_and_float_integer(self):
        with self.assertRaises(PredicateError):
            typed_batch([X_INT], [{"x": 1, "y": 2}])
        with self.assertRaises(PredicateError):
            typed_batch([X_INT], [{"x": 1.0}])

    def test_boolean_equality(self):
        stats = ColumnStats(null_count=0, value_count=2, minimum=False, maximum=True)
        block = Block(2, {FLAG: stats})
        predicate = Comparison(FLAG, Op.EQ, Literal(True))
        decision = analyze_block(predicate, block)
        self.assertFalse(decision.skip)

    def test_empty_block_has_empty_possible_outcomes(self):
        block = Block(0, {X_INT: ColumnStats(0)})
        decision = analyze_block(IsNull(X_INT), block)
        self.assertTrue(decision.skip)
        self.assertEqual(decision.reason_code, "EMPTY_BLOCK")

    def test_compound_or_and_not(self):
        stats = ColumnStats(null_count=1, value_count=1, minimum=1, maximum=1)
        block = Block(2, {X_INT: stats})
        predicate = Or(
            Comparison(X_INT, Op.GT, Literal(2)),
            Not(Comparison(X_INT, Op.GT, Literal(2))),
        )
        decision = analyze_block(predicate, block)
        self.assertFalse(decision.skip)
        self.assertEqual(
            decision.possible_outcomes,
            frozenset({TriBool.TRUE, TriBool.UNKNOWN}),
        )

        conjunction = And(
            Comparison(X_INT, Op.GT, Literal(0)),
            Comparison(X_INT, Op.LT, Literal(0)),
        )
        self.assertTrue(analyze_block(conjunction, block).skip)

    def test_stats_reject_nan_in_min_max(self):
        with self.assertRaises(ValueError):
            ColumnStats(null_count=0, nan_count=1, minimum=math.nan, maximum=math.nan)

    def test_integer_stats_reject_nan_count(self):
        stats = ColumnStats(null_count=0, nan_count=1, value_count=0)
        block = Block(1, {X_INT: stats})
        predicate = Comparison(X_INT, Op.GT, Literal(2))
        with self.assertRaises(ValueError):
            analyze_block(predicate, block)


class ScanAuditTests(unittest.TestCase):
    def test_pruned_scan_matches_reference(self):
        batches = [
            typed_batch([X_INT], [{"x": None}, {"x": None}]),
            typed_batch([X_INT], [{"x": 1}, {"x": 3}]),
            typed_batch([X_INT], [{"x": 10}, {"x": 11}]),
        ]
        predicate = Comparison(X_INT, Op.GT, Literal(2))
        reference = scan_row_by_row(batches, predicate)
        pruned = scan_with_pruning(batches, predicate)
        report = audit_pruning(reference, pruned)
        self.assertTrue(report.ok)
        self.assertEqual(report.reference_selected, ((1, 1), (2, 0), (2, 1)))
        self.assertTrue(pruned[0].skipped)
        self.assertFalse(pruned[1].skipped)
        self.assertFalse(pruned[2].skipped)

    def test_randomized_block_skips_agree_with_row_interpreter(self):
        random.seed(9137)
        predicates = [
            Comparison(X_INT, op, Literal(literal))
            for op in (Op.EQ, Op.NE, Op.LT, Op.LE, Op.GT, Op.GE)
            for literal in (-3, 0, 2, 7)
        ]
        predicates.extend(
            [
                IsNull(X_INT),
                IsNotNull(X_INT),
                Not(Comparison(X_INT, Op.GT, Literal(2))),
                And(Comparison(X_INT, Op.GE, Literal(0)), IsNull(X_INT)),
                Or(Comparison(X_INT, Op.LT, Literal(0)), IsNull(X_INT)),
            ]
        )

        for _ in range(100):
            batches = []
            for block_id in range(4):
                rows = [
                    {"x": random.choice([None, random.randint(-5, 10)])}
                    for _ in range(random.randint(0, 8))
                ]
                batches.append(typed_batch([X_INT], rows))
            for predicate in predicates:
                reference = scan_row_by_row(batches, predicate)
                pruned = scan_with_pruning(batches, predicate)
                report = audit_pruning(reference, pruned)
                self.assertTrue(report.ok, (block_id, predicate))

    def test_randomized_float_blocks_including_nan_agree_with_reference(self):
        random.seed(4041)
        predicates = [
            Comparison(X_FLOAT, op, Literal(literal, ColumnType.FLOAT))
            for op in (Op.EQ, Op.NE, Op.LT, Op.LE, Op.GT, Op.GE)
            for literal in (-2.5, 0.0, 2.0, 6.25)
        ]
        predicates.extend(
            [
                Not(Comparison(X_FLOAT, Op.GT, Literal(2.0, ColumnType.FLOAT))),
                IsNull(X_FLOAT),
                IsNotNull(X_FLOAT),
            ]
        )
        for _ in range(100):
            batches = []
            for _ in range(4):
                rows = []
                for _ in range(random.randint(0, 8)):
                    choice = random.randrange(4)
                    if choice == 0:
                        rows.append({"x": None})
                    elif choice == 1:
                        rows.append({"x": math.nan})
                    else:
                        rows.append({"x": float(random.randint(-3, 7)) + random.choice([0.0, 0.25, 0.5])})
                batches.append(typed_batch([X_FLOAT], rows))
            for predicate in predicates:
                reference = scan_row_by_row(batches, predicate)
                pruned = scan_with_pruning(batches, predicate)
                report = audit_pruning(reference, pruned)
                self.assertTrue(report.ok, predicate)


if __name__ == "__main__":
    unittest.main(verbosity=2)
