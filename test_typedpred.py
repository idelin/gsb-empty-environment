import math
import random
import unittest

from typedpred import (
    Batch,
    Col,
    Schema,
    TVL,
    Type,
    TypeMismatchError,
    cross_check,
    prune_decision,
    row_scan,
)

NAN = math.nan


class ThreeValuedLogicTests(unittest.TestCase):
    def test_not_truth_table(self):
        self.assertEqual(~TVL.TRUE, TVL.FALSE)
        self.assertEqual(~TVL.FALSE, TVL.TRUE)
        # The required rule: NOT UNKNOWN is UNKNOWN.
        self.assertEqual(~TVL.UNKNOWN, TVL.UNKNOWN)

    def test_and_truth_table(self):
        self.assertEqual(TVL.TRUE & TVL.TRUE, TVL.TRUE)
        self.assertEqual(TVL.FALSE & TVL.UNKNOWN, TVL.FALSE)
        self.assertEqual(TVL.TRUE & TVL.UNKNOWN, TVL.UNKNOWN)
        self.assertEqual(TVL.UNKNOWN & TVL.UNKNOWN, TVL.UNKNOWN)

    def test_or_truth_table(self):
        self.assertEqual(TVL.FALSE | TVL.FALSE, TVL.FALSE)
        self.assertEqual(TVL.TRUE | TVL.UNKNOWN, TVL.TRUE)
        self.assertEqual(TVL.FALSE | TVL.UNKNOWN, TVL.UNKNOWN)


class NullVsNaNTests(unittest.TestCase):
    def setUp(self):
        self.schema = Schema({"x": Type.FLOAT})

    def test_null_comparison_is_unknown(self):
        b = Batch(self.schema, {"x": [None]})
        _, rows, verdicts = row_scan(b, Col("x") > 2)
        self.assertEqual(verdicts, (TVL.UNKNOWN,))
        self.assertEqual(rows, ())

    def test_nan_is_not_null(self):
        b = Batch(self.schema, {"x": [NAN]})
        _, rows, verdicts = row_scan(b, Col("x").is_null())
        self.assertEqual(verdicts, (TVL.FALSE,))
        _, rows, verdicts = row_scan(b, Col("x").is_not_null())
        self.assertEqual(verdicts, (TVL.TRUE,))

    def test_nan_comparison_ieee(self):
        b = Batch(self.schema, {"x": [NAN]})
        for op, lit in [(">", 2.0), ("<", 2.0), ("=", 2.0), (">=", 2.0)]:
            pred = {
                ">": Col("x") > 2.0,
                "<": Col("x") < 2.0,
                "=": Col("x").eq(2.0),
                ">=": Col("x") >= 2.0,
            }[op]
            _, _, verdicts = row_scan(b, pred)
            self.assertEqual(verdicts, (TVL.FALSE,), f"NaN {op} 2 must be FALSE")
        _, rows, verdicts = row_scan(b, Col("x").ne(2.0))
        self.assertEqual(verdicts, (TVL.TRUE,))  # NaN != c is TRUE

    def test_not_unknown_row(self):
        b = Batch(self.schema, {"x": [None]})
        _, _, verdicts = row_scan(b, ~(Col("x") > 2))
        self.assertEqual(verdicts, (TVL.UNKNOWN,))
        result = cross_check(b, ~(Col("x") > 2))
        self.assertEqual(result.matched_rows, ())


class AllNullBlockTests(unittest.TestCase):
    def setUp(self):
        self.batch = Batch(Schema({"x": Type.INT}), {"x": [None, None]})

    def test_all_null_gt_is_skippable(self):
        d = prune_decision(self.batch, Col("x") > 2)
        self.assertTrue(d.skipped)
        self.assertEqual(d.verdict, TVL.FALSE)
        result = cross_check(self.batch, Col("x") > 2)
        self.assertEqual(result.matched_rows, ())

    def test_all_null_is_null_not_skippable(self):
        d = prune_decision(self.batch, Col("x").is_null())
        self.assertFalse(d.skipped)
        result = cross_check(self.batch, Col("x").is_null())
        self.assertEqual(len(result.matched_rows), 2)

    def test_all_null_is_not_null_skippable(self):
        d = prune_decision(self.batch, Col("x").is_not_null())
        self.assertTrue(d.skipped)


class NaNStatsTests(unittest.TestCase):
    def setUp(self):
        self.schema = Schema({"x": Type.FLOAT})

    def test_nan_before_match_must_not_prune(self):
        # Naive min/max that absorbs NaN could "prove" empty here.
        b = Batch(self.schema, {"x": [NAN, 5.0, 1.0]})
        d = prune_decision(b, Col("x") > 2)
        self.assertFalse(d.skipped)
        result = cross_check(b, Col("x") > 2)
        self.assertEqual(result.matched_indexes, (1,))

    def test_nan_after_match_must_not_prune(self):
        b = Batch(self.schema, {"x": [5.0, 1.0, NAN]})
        d = prune_decision(b, Col("x") > 2)
        self.assertFalse(d.skipped)
        result = cross_check(b, Col("x") > 2)
        self.assertEqual(result.matched_indexes, (0,))

    def test_nan_with_only_low_values_is_safe_skip(self):
        b = Batch(self.schema, {"x": [NAN, 1.0, 0.5]})
        d = prune_decision(b, Col("x") > 2)
        self.assertTrue(d.skipped)
        cross_check(b, Col("x") > 2)  # must not raise

    def test_nan_block_is_null_skips_is_not_null_does_not(self):
        b = Batch(self.schema, {"x": [NAN, NAN]})
        self.assertTrue(prune_decision(b, Col("x").is_null()).skipped)
        self.assertFalse(prune_decision(b, Col("x").is_not_null()).skipped)

    def test_nan_ne_literal_matches(self):
        b = Batch(self.schema, {"x": [NAN, 5.0]})
        result = cross_check(b, Col("x").ne(2.0))
        self.assertEqual(result.matched_indexes, (0, 1))
        b2 = Batch(self.schema, {"x": [NAN, None]})
        # NaN != 2 TRUE, null UNKNOWN -> cannot skip, one match.
        self.assertFalse(prune_decision(b2, Col("x").ne(2.0)).skipped)
        result = cross_check(b2, Col("x").ne(2.0))
        self.assertEqual(result.matched_indexes, (0,))

    def test_stats_minmax_exclude_nan(self):
        s = Batch(self.schema, {"x": [NAN, 5.0, 1.0]}).stats("x")
        self.assertEqual((s.nan_count, s.null_count, s.valid_count), (1, 0, 2))
        self.assertEqual(s.min, 1.0)
        self.assertEqual(s.max, 5.0)


class CompoundPruneTests(unittest.TestCase):
    def setUp(self):
        self.schema = Schema({"x": Type.INT})

    def test_and_skips_if_one_branch_empty(self):
        b = Batch(self.schema, {"x": [1, 2]})
        pred = (Col("x") > 2) & Col("x").is_null()
        self.assertTrue(prune_decision(b, pred).skipped)

    def test_or_skips_only_if_both_empty(self):
        b = Batch(self.schema, {"x": [1, 2]})
        pred = (Col("x") > 100) | (Col("x") < 0)
        self.assertTrue(prune_decision(b, pred).skipped)
        b2 = Batch(self.schema, {"x": [1, 2, None]})
        self.assertFalse(
            prune_decision(b2, (Col("x") > 100) | Col("x").is_null()).skipped
        )

    def test_not_prunes_when_operand_always_true(self):
        b = Batch(self.schema, {"x": [5, 7]})
        self.assertTrue(prune_decision(b, ~(Col("x") > 2)).skipped)

    def test_not_unknown_block_must_scan(self):
        b = Batch(self.schema, {"x": [5, None]})
        self.assertFalse(prune_decision(b, ~(Col("x") > 2)).skipped)
        result = cross_check(b, ~(Col("x") > 2))
        self.assertEqual(result.matched_indexes, ())  # FALSE and null(UNKNOWN)


class ExactIntegerTests(unittest.TestCase):
    def test_huge_integer_exact(self):
        big = 2 ** 127
        b = Batch(Schema({"x": Type.INT}), {"x": [big, big + 1, big - 1]})
        result = cross_check(b, Col("x").eq(big + 1))
        self.assertEqual(result.matched_indexes, (1,))
        self.assertTrue(prune_decision(b, Col("x") > big + 1).skipped)
        self.assertFalse(prune_decision(b, Col("x") >= big + 1).skipped)

    def test_reject_float_in_int_column(self):
        with self.assertRaises(TypeMismatchError):
            Batch(Schema({"x": Type.INT}), {"x": [1.5]})

    def test_reject_incomparable_types(self):
        b = Batch(Schema({"x": Type.INT}), {"x": [1, 2]})
        with self.assertRaises(TypeMismatchError):
            prune_decision(b, Col("x").eq("a"))


class RandomizedSoundnessTests(unittest.TestCase):
    """Property: whenever the pruner skips, zero rows evaluate TRUE."""

    def test_random_batches_and_predicates(self):
        rng = random.Random(1234)
        int_schema = Schema({"x": Type.INT})
        float_schema = Schema({"x": Type.FLOAT})

        def random_int_block():
            return Batch(
                int_schema,
                {"x": [rng.choice([None, rng.randint(0, 9)]) for _ in range(rng.randint(1, 12))]},
            )

        def random_float_block():
            vals = []
            for _ in range(rng.randint(1, 12)):
                pick = rng.random()
                if pick < 0.2:
                    vals.append(None)
                elif pick < 0.35:
                    vals.append(NAN)
                else:
                    vals.append(float(rng.randint(0, 9)))
            return Batch(float_schema, {"x": vals})

        int_preds = [
            lambda: Col("x") > rng.randint(0, 9),
            lambda: Col("x").eq(rng.randint(0, 9)),
            lambda: Col("x").ne(rng.randint(0, 9)),
            lambda: Col("x") <= rng.randint(0, 9),
            lambda: Col("x").is_null(),
            lambda: Col("x").is_not_null(),
        ]
        float_preds = [
            lambda: Col("x") > float(rng.randint(0, 9)),
            lambda: Col("x").ne(float(rng.randint(0, 9))),
            lambda: Col("x").is_null(),
        ]

        checked = 0
        for _ in range(600):
            use_float = rng.random() < 0.5
            batch = random_float_block() if use_float else random_int_block()
            makers = float_preds if use_float else int_preds
            leaf = rng.choice(makers)()
            pred = leaf
            if rng.random() < 0.3:
                pred = ~pred
            other = rng.choice(makers)()
            if rng.random() < 0.4:
                pred = pred & other
            elif rng.random() < 0.4:
                pred = pred | other

            decision = prune_decision(batch, pred)
            indexes, _, verdicts = row_scan(batch, pred)
            if decision.skipped:
                self.assertNotIn(TVL.TRUE, verdicts)
                self.assertEqual(indexes, ())
                self.assertIsNotNone(decision.reason)
            checked += 1
        self.assertGreater(checked, 300)


if __name__ == "__main__":
    unittest.main(verbosity=2)
