import math
import unittest

from bitmap import Bitmap, BitmapLengthError
from table import ColType, SchemaError, Table


def make_table():
    t = Table({"id": ColType.INT, "temp": ColType.FLOAT,
               "city": ColType.STRING})
    t.add_batch({
        "id": [1, 2, 3, 4],
        "temp": [21.5, float("nan"), None, 35.0],
        "city": ["beijing", "shanghai", None, "beijing"],
    })
    t.add_batch({
        "id": [5, 6],
        "temp": [28.0, 19.5],
        "city": ["shenzhen", "beijing"],
    })
    return t


class BatchIngestTest(unittest.TestCase):
    def test_batches_accumulate(self):
        t = make_table()
        self.assertEqual(t.num_rows, 6)
        self.assertEqual(t.num_batches, 2)
        self.assertEqual(t.schema["id"], ColType.INT)

    def test_type_validation(self):
        t = Table({"a": ColType.INT, "b": ColType.STRING})
        with self.assertRaises(SchemaError):
            t.add_batch({"a": [1.5], "b": ["x"]})          # float in INT col
        with self.assertRaises(SchemaError):
            t.add_batch({"a": [True], "b": ["x"]})         # bool is not INT
        with self.assertRaises(SchemaError):
            t.add_batch({"a": [1], "b": [2]})              # non-str in STRING
        with self.assertRaises(SchemaError):
            t.add_batch({"a": [1, 2], "b": ["x"]})         # ragged batch
        with self.assertRaises(SchemaError):
            t.add_batch({"a": [1], "b": ["x"], "c": [0]})  # unknown column
        with self.assertRaises(SchemaError):
            t.add_batch({"a": [1]})                        # missing column
        self.assertEqual(t.num_rows, 0)  # failed batches leave no residue

    def test_float_column_accepts_int_and_nan(self):
        t = Table({"x": ColType.FLOAT})
        t.add_batch({"x": [1, 2.5, float("nan"), None]})
        self.assertEqual(t.num_rows, 4)

    def test_empty_batch_and_empty_table(self):
        t = Table({"a": ColType.INT})
        t.add_batch({"a": []})
        self.assertEqual(t.num_rows, 0)
        bm = t.where(t.col("a") == 1)
        self.assertEqual(len(bm), 0)
        self.assertEqual(bm.count(), 0)
        self.assertEqual(t.filter(bm), [])


class NullAndNanTest(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        t = make_table()
        is_null = t.where(t.col("temp").is_null())
        is_nan = t.where(t.col("temp").is_nan())
        self.assertEqual(is_null.to_indices(), [2])
        self.assertEqual(is_nan.to_indices(), [1])
        self.assertEqual((is_null & is_nan).count(), 0)
        self.assertEqual(t.where(t.col("temp").is_valid()).to_indices(),
                         [0, 1, 3, 4, 5])  # NaN is valid (present), null is not

    def test_comparisons_exclude_null_and_nan(self):
        t = make_table()
        self.assertEqual(t.where(t.col("temp") >= 20.0).to_indices(),
                         [0, 3, 4])
        self.assertEqual(t.where(t.col("temp") == float("nan")).count(), 0)
        # IEEE: nan != anything is true, but null still never matches
        self.assertEqual(t.where(t.col("temp") != 21.5).to_indices(),
                         [1, 3, 4, 5])
        # null city matches neither == nor !=
        self.assertEqual(t.where(t.col("city") == "beijing").to_indices(),
                         [0, 3, 5])
        self.assertEqual(t.where(t.col("city") != "beijing").to_indices(),
                         [1, 4])


class ExactIntTest(unittest.TestCase):
    def test_big_int_arithmetic_is_exact(self):
        t = Table({"n": ColType.INT})
        t.add_batch({"n": [2 ** 62, 2 ** 62 + 1, None]})
        # 2**62 and 2**62+1 collapse to the same float; ints stay exact
        self.assertEqual(t.where(t.col("n") == 2 ** 62 + 1).to_indices(), [1])
        doubled = t.col("n") * 2
        self.assertEqual(t.where(doubled == 2 ** 63 + 2).to_indices(), [1])
        self.assertEqual(t.where(t.col("n") + 1 == 2 ** 62 + 2).to_indices(),
                         [1])
        self.assertEqual(t.where(t.col("n") - 2 ** 62 == 1).to_indices(), [1])
        # arithmetic with null propagates null -> comparison is false
        self.assertEqual(t.where(doubled > 0).to_indices(), [0, 1])

    def test_arithmetic_on_strings_rejected(self):
        t = Table({"s": ColType.STRING})
        t.add_batch({"s": ["a"]})
        with self.assertRaises(SchemaError):
            t.where((t.col("s") + 1) == 2)


class SelectionTest(unittest.TestCase):
    def test_composed_selection(self):
        t = make_table()
        pred = (t.col("city") == "beijing") & ~(t.col("temp") < 30.0)
        bm = t.where(pred)
        self.assertEqual(bm.to_indices(), [3])
        rows = t.filter(pred)
        self.assertEqual(rows, [{"id": 4, "temp": 35.0, "city": "beijing"}])

    def test_or_and_not_over_valid_range(self):
        t = make_table()
        bm = t.where((t.col("city") == "beijing") | (t.col("temp") < 20.0))
        self.assertEqual(bm.to_indices(), [0, 3, 5])
        neg = t.where(~(t.col("city") == "beijing"))
        self.assertEqual(neg.to_indices(), [1, 2, 4])
        self.assertEqual(neg.count() + t.where(
            t.col("city") == "beijing").count(), t.num_rows)

    def test_bitmap_and_predicate_interop(self):
        t = make_table()
        bm = t.where(t.col("city") == "beijing")
        self.assertEqual(bm & Bitmap.ones(t.num_rows), bm)
        self.assertEqual(bm | Bitmap.zeros(t.num_rows), bm)
        rows = t.filter(bm & t.where(t.col("id") > 1))
        self.assertEqual([r["id"] for r in rows], [4, 6])

    def test_filter_length_mismatch_raises(self):
        t = make_table()
        with self.assertRaises(BitmapLengthError):
            t.filter(Bitmap.zeros(3))
        with self.assertRaises(BitmapLengthError):
            t.filter(Bitmap.ones(7))

    def test_rank_select_maps_rows(self):
        t = make_table()
        bm = t.where(t.col("city") == "beijing")
        rows = t.filter(bm)
        for pos, i in enumerate(bm):
            self.assertEqual(bm.select(bm.rank(i)), i)
            self.assertEqual(bm.select(pos + 1), i)
        self.assertEqual([r["id"] for r in rows], [1, 4, 6])

    def test_unknown_column_raises(self):
        t = make_table()
        with self.assertRaises(SchemaError):
            t.col("nope")


if __name__ == "__main__":
    unittest.main()
