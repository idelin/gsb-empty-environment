import math
import unittest

from pivot_engine import Batch, PivotEngine


class PivotBasicTest(unittest.TestCase):
    def test_basic_pivot_and_stable_column_order(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[
            ("k1", "B", 1),
            ("k1", "A", 2),
            ("k2", "A", 3),
        ]))
        snap = eng.snapshot()
        self.assertEqual(snap.columns, ("key", "B", "A"))  # first-seen order
        self.assertEqual(snap.rows, (("k1", 1, 2), ("k2", None, 3)))

    def test_conflict_rows_are_summed(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[
            ("k", "A", 4),
            ("k", "A", 6),
        ]))
        self.assertEqual(eng.snapshot().rows, (("k", 10),))

    def test_conflict_across_batches_are_summed(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", 4)]))
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", 6)]))
        self.assertEqual(eng.snapshot().rows, (("k", 10),))


class SchemaEvolutionTest(unittest.TestCase):
    def test_new_category_emits_event_and_backfills_null(self):
        eng = PivotEngine()
        r1 = eng.apply(Batch("str", "str", "int", rows=[("k1", "A", 1)]))
        self.assertTrue(r1.schema_changed)
        self.assertEqual(r1.schema_version, 1)
        self.assertEqual(r1.added_columns, ("A",))

        r2 = eng.apply(Batch("str", "str", "int", rows=[("k2", "B", 2)]))
        self.assertTrue(r2.schema_changed)
        self.assertEqual(r2.added_columns, ("B",))
        self.assertEqual(r2.schema_version, 2)

        self.assertEqual(len(eng.events), 2)
        ev = eng.events[-1]
        self.assertEqual(ev.version, 2)
        self.assertEqual(ev.added_columns, ("B",))
        self.assertEqual(ev.columns, ("A", "B"))

        snap = eng.snapshot()
        self.assertEqual(snap.columns, ("key", "A", "B"))
        # old row k1 gets null for the new category B
        self.assertEqual(snap.rows, (("k1", 1, None), ("k2", None, 2)))

    def test_no_new_category_means_no_event(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k1", "A", 1)]))
        r2 = eng.apply(Batch("str", "str", "int", rows=[("k2", "A", 2)]))
        self.assertFalse(r2.schema_changed)
        self.assertEqual(len(eng.events), 1)
        self.assertEqual(eng.schema_version, 1)

    def test_align_old_batch_to_new_schema(self):
        eng = PivotEngine()
        r1 = eng.apply(Batch("str", "str", "int",
                             rows=[("k1", "A", 1), ("k1", "B", 2)]))
        eng.apply(Batch("str", "str", "int", rows=[("k2", "C", 3)]))
        # values emitted under v1 (columns A, B) align to current schema
        aligned = eng.align_values([1, 2], from_version=r1.schema_version)
        self.assertEqual(aligned, [1, 2, None])
        # explicit target version: project back down to v1
        self.assertEqual(
            eng.align_values([1, 2, None], from_version=2, to_version=1),
            [1, 2],
        )
        with self.assertRaises(ValueError):
            eng.align_values([1], from_version=1)  # wrong arity
        with self.assertRaises(ValueError):
            eng.align_values([1, 2], from_version=99)  # unknown version

    def test_column_order_stable_after_full_retraction(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int",
                        rows=[("k", "A", 1), ("k", "B", 2)]))
        eng.apply(Batch("str", "str", "int",
                        rows=[("-", "k", "A", 1), ("-", "k", "B", 2)]))
        snap = eng.snapshot()
        self.assertEqual(snap.columns, ("key", "A", "B"))  # schema never shrinks
        self.assertEqual(snap.rows, ())


class RetractionTest(unittest.TestCase):
    def test_retract_one_contribution_keeps_other(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", 4), ("k", "A", 6)]))
        eng.apply(Batch("str", "str", "int", rows=[("-", "k", "A", 4)]))
        self.assertEqual(eng.snapshot().rows, (("k", 6),))

    def test_retract_last_row_of_category_keeps_other_categories(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", 1), ("k", "B", 2)]))
        eng.apply(Batch("str", "str", "int", rows=[("-", "k", "B", 2)]))
        snap = eng.snapshot()
        self.assertEqual(snap.columns, ("key", "A", "B"))
        self.assertEqual(snap.rows, (("k", 1, None),))  # key row survives

    def test_retract_last_cell_removes_key(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", 1)]))
        eng.apply(Batch("str", "str", "int", rows=[("-", "k", "A", 1)]))
        self.assertEqual(eng.snapshot().rows, ())

    def test_retract_without_live_row_raises(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", 1)]))
        with self.assertRaises(KeyError):
            eng.apply(Batch("str", "str", "int", rows=[("-", "k", "A", 2)]))
        with self.assertRaises(KeyError):
            eng.apply(Batch("str", "str", "int", rows=[("-", "ghost", "A", 1)]))
        with self.assertRaises(KeyError):
            eng.apply(Batch("str", "str", "int", rows=[("-", "k", "B", 1)]))
        # failed retractions must not corrupt state
        self.assertEqual(eng.snapshot().rows, (("k", 1),))


class NullAndNaNTest(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "float", rows=[
            ("k", "NUL", None),
            ("k", "NAN", float("nan")),
            ("k", "NUM", 1.5),
        ]))
        row = eng.snapshot().rows[0]
        self.assertIsNone(row[1])
        self.assertTrue(math.isnan(row[2]))
        self.assertEqual(row[3], 1.5)

    def test_null_skipped_in_sum_but_nan_poisons(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "float", rows=[
            ("k", "A", None), ("k", "A", 2.0),
            ("k", "B", float("nan")), ("k", "B", 2.0),
        ]))
        row = eng.snapshot().rows[0]
        self.assertEqual(row[1], 2.0)
        self.assertTrue(math.isnan(row[2]))

    def test_sum_of_only_nulls_is_null(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", None)]))
        self.assertIsNone(eng.snapshot().rows[0][1])

    def test_retract_nan_restores_value(self):
        eng = PivotEngine()
        nan = float("nan")
        eng.apply(Batch("str", "str", "float",
                        rows=[("k", "A", nan), ("k", "A", 2.0)]))
        self.assertTrue(math.isnan(eng.snapshot().rows[0][1]))
        eng.apply(Batch("str", "str", "float", rows=[("-", "k", "A", nan)]))
        self.assertEqual(eng.snapshot().rows[0][1], 2.0)

    def test_null_rows_are_retractable(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "int",
                        rows=[("k", "A", None), ("k", "A", 5)]))
        eng.apply(Batch("str", "str", "int", rows=[("-", "k", "A", None)]))
        self.assertEqual(eng.snapshot().rows, (("k", 5),))


class NumericExactnessTest(unittest.TestCase):
    def test_integer_sum_is_exact(self):
        eng = PivotEngine()
        big = 2**70 + 1
        eng.apply(Batch("str", "str", "int", rows=[("k", "A", big), ("k", "A", big)]))
        got = eng.snapshot().rows[0][1]
        self.assertIsInstance(got, int)
        self.assertEqual(got, 2 * big)

    def test_mixed_int_float_widens_only_while_float_present(self):
        eng = PivotEngine()
        eng.apply(Batch("str", "str", "float", rows=[("k", "A", 1), ("k", "A", 0.5)]))
        self.assertEqual(eng.snapshot().rows[0][1], 1.5)
        eng.apply(Batch("str", "str", "float", rows=[("-", "k", "A", 0.5)]))
        got = eng.snapshot().rows[0][1]
        self.assertEqual(got, 1)
        self.assertIsInstance(got, int)

    def test_count_aggregation_ignores_null(self):
        eng = PivotEngine(agg="count")
        eng.apply(Batch("str", "str", "int",
                        rows=[("k", "A", 1), ("k", "A", None), ("k", "A", 2)]))
        self.assertEqual(eng.snapshot().rows[0][1], 2)
        eng.apply(Batch("str", "str", "int", rows=[("-", "k", "A", 1)]))
        self.assertEqual(eng.snapshot().rows[0][1], 1)


class ValidationTest(unittest.TestCase):
    def test_declared_types_are_enforced(self):
        eng = PivotEngine()
        with self.assertRaises(TypeError):
            eng.apply(Batch("str", "str", "int", rows=[("k", "A", "nope")]))
        with self.assertRaises(TypeError):
            eng.apply(Batch("str", "str", "int", rows=[("k", "A", True)]))
        with self.assertRaises(TypeError):
            eng.apply(Batch("str", "str", "int", rows=[("k", "A", 1.5)]))

    def test_null_and_nan_dimensions_rejected(self):
        eng = PivotEngine()
        with self.assertRaises(ValueError):
            eng.apply(Batch("str", "str", "int", rows=[(None, "A", 1)]))
        with self.assertRaises(ValueError):
            eng.apply(Batch("str", "str", "int", rows=[("k", None, 1)]))
        with self.assertRaises(ValueError):
            eng.apply(Batch("str", "str", "int", rows=[("k", float("nan"), 1)]))

    def test_unknown_types_and_aggs_rejected(self):
        with self.assertRaises(ValueError):
            PivotEngine(agg="median")
        eng = PivotEngine()
        with self.assertRaises(ValueError):
            eng.apply(Batch("str", "str", "unknown", rows=[("k", "A", 1)]))
        with self.assertRaises(TypeError):
            eng.apply(Batch("str", "str", "str", rows=[("k", "A", "x")]))

    def test_bad_row_shape_and_op_rejected(self):
        eng = PivotEngine()
        with self.assertRaises(ValueError):
            eng.apply(Batch("str", "str", "int", rows=[("k", "A")]))
        with self.assertRaises(ValueError):
            eng.apply(Batch("str", "str", "int", rows=[("?", "k", "A", 1)]))


if __name__ == "__main__":
    unittest.main()
