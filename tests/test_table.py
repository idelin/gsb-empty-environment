import math
import unittest

from rowselect import Batch, Bitmap, Schema, Table, col


def make_table():
    schema = Schema([("id", "int"), ("price", "float"), ("tag", "str")])
    nan = float("nan")
    batch1 = Batch(schema, [
        (1, 10.0, "a"),
        (2, None, "b"),      # null price
        (3, nan, "a"),       # NaN price (not null!)
    ])
    batch2 = Batch(schema, [
        (4, 25.5, "c"),
        (5, 7.25, "b"),
        (6, None, None),     # null price and null tag
    ])
    return Table(schema, [batch1, batch2])


class TestSchemaAndBatches(unittest.TestCase):
    def test_row_count_across_batches(self):
        table = make_table()
        self.assertEqual(table.num_rows, 6)
        self.assertEqual(len(table.batches), 2)

    def test_type_validation(self):
        schema = Schema([("n", "int")])
        with self.assertRaises(TypeError):
            Batch(schema, [("not-an-int",)])
        with self.assertRaises(TypeError):
            Batch(schema, [(True,)])  # bool is not int here
        with self.assertRaises(TypeError):
            Batch(schema, [(1.5,)])

    def test_none_allowed_for_any_column(self):
        schema = Schema([("n", "int"), ("s", "str"), ("f", "float")])
        batch = Batch(schema, [(None, None, None)])
        self.assertEqual(len(batch), 1)

    def test_wrong_arity_rejected(self):
        schema = Schema([("n", "int")])
        with self.assertRaises(ValueError):
            Batch(schema, [(1, 2)])

    def test_dict_rows_accepted(self):
        schema = Schema([("n", "int"), ("s", "str")])
        batch = Batch(schema, [{"n": 1, "s": "x"}])
        self.assertEqual(batch.rows[0], (1, "x"))

    def test_schema_mismatch_rejected(self):
        table = make_table()
        other = Batch(Schema([("id", "int")]), [(1,)])
        with self.assertRaises(ValueError):
            table.add_batch(other)

    def test_unknown_column_type_rejected(self):
        with self.assertRaises(ValueError):
            Schema([("x", "decimal")])


class TestNullVsNaN(unittest.TestCase):
    def setUp(self):
        self.table = make_table()

    def test_comparison_skips_null_and_nan(self):
        bm = self.table.where(col("price") > 8.0)
        # rows: 10.0 (yes), null (no), NaN (no), 25.5 (yes), 7.25 (no), null (no)
        self.assertEqual(bm.to_indices(), [0, 3])

    def test_nan_compares_unequal_to_everything(self):
        bm = self.table.where(col("price") == col("price"))
        # null rows and the NaN row are all excluded
        self.assertEqual(bm.to_indices(), [0, 3, 4])

    def test_is_null_vs_is_nan(self):
        nulls = self.table.where(col("price").is_null())
        nans = self.table.where(col("price").is_nan())
        self.assertEqual(nulls.to_indices(), [1, 5])
        self.assertEqual(nans.to_indices(), [2])
        # null and NaN are disjoint
        self.assertEqual((nulls & nans).popcount(), 0)

    def test_is_not_null_includes_nan(self):
        bm = self.table.where(col("price").is_not_null())
        self.assertEqual(bm.to_indices(), [0, 2, 3, 4])

    def test_null_tag(self):
        bm = self.table.where(col("tag").is_null())
        self.assertEqual(bm.to_indices(), [5])


class TestExactIntegers(unittest.TestCase):
    def test_big_ints_stay_exact(self):
        schema = Schema([("n", "int")])
        big = 2**63 + 12345  # beyond float64 integer precision
        table = Table(schema, [Batch(schema, [(big,), (big + 2,), (1,)])])
        bm = table.where(col("n") == big)
        self.assertEqual(bm.to_indices(), [0])
        bm = table.where(col("n") > big)
        self.assertEqual(bm.to_indices(), [1])

    def test_int_float_boundary_is_exact(self):
        # 2**53 + 1 is not representable as float; comparison must not round.
        schema = Schema([("n", "int")])
        table = Table(schema, [Batch(schema, [(2**53 + 1,)])])
        bm = table.where(col("n") == 2**53 + 1)
        self.assertEqual(bm.to_indices(), [0])
        bm = table.where(col("n") > float(2**53))
        self.assertEqual(bm.to_indices(), [0])


class TestComposition(unittest.TestCase):
    def setUp(self):
        self.table = make_table()

    def test_and_or_not_on_expressions(self):
        expr = (col("price") > 8.0) & (col("tag") == "a")
        self.assertEqual(self.table.where(expr).to_indices(), [0])
        expr = (col("tag") == "a") | (col("tag") == "c")
        self.assertEqual(self.table.where(expr).to_indices(), [0, 2, 3])
        expr = ~(col("tag") == "b")
        self.assertEqual(self.table.where(expr).to_indices(), [0, 2, 3, 5])

    def test_bitmap_level_composition(self):
        cheap = self.table.where(col("price") < 20.0)
        tagged_a = self.table.where(col("tag") == "a")
        self.assertEqual((cheap & tagged_a).to_indices(), [0])
        self.assertEqual((cheap | tagged_a).to_indices(), [0, 2, 4])
        self.assertEqual((~cheap).to_indices(), [1, 2, 3, 5])

    def test_cross_table_length_mismatch_raises(self):
        other = Table(self.table.schema,
                      [Batch(self.table.schema, [(9, 1.0, "z")])])
        bm6 = self.table.where(col("tag") == "a")
        bm1 = other.where(col("tag") == "z")
        with self.assertRaises(ValueError):
            bm6 & bm1
        with self.assertRaises(ValueError):
            bm6 | bm1

    def test_fetch_selected_rows(self):
        bm = self.table.where(col("tag") == "b")
        self.assertEqual(self.table.fetch(bm), [(2, None, "b"), (5, 7.25, "b")])

    def test_fetch_length_mismatch_raises(self):
        with self.assertRaises(ValueError):
            self.table.fetch(Bitmap.zeros(3))

    def test_empty_table(self):
        table = Table(self.table.schema)
        bm = table.where(col("price") > 0.0)
        self.assertEqual(len(bm), 0)
        self.assertEqual(bm.popcount(), 0)
        self.assertEqual(table.fetch(bm), [])

    def test_rank_select_on_query_result(self):
        bm = self.table.where(col("price").is_not_null())
        for i in bm:
            self.assertEqual(bm.select(bm.rank(i)), i)


if __name__ == "__main__":
    unittest.main()
