import math
import unittest

from schemamigrate import (
    NULL,
    Batch,
    BatchError,
    Schema,
    TypeTag,
    make_column,
    values_equal,
)


class BatchTest(unittest.TestCase):
    def setUp(self):
        self.c_int = make_column("n", TypeTag.INT, nullable=False)
        self.c_float = make_column("f", TypeTag.FLOAT)
        self.schema = Schema([self.c_int, self.c_float])

    def test_null_and_nan_are_distinct(self):
        batch = Batch(self.schema, {"n": [1, 2], "f": [NULL, float("nan")]})
        values = batch.column_values("f")
        self.assertIs(values[0], NULL)
        self.assertTrue(math.isnan(values[1]))
        self.assertIsNot(values[1], NULL)
        self.assertFalse(values_equal(values[0], values[1]))
        self.assertTrue(values_equal(float("nan"), values[1]))
        self.assertTrue(values_equal(NULL, NULL))

    def test_integer_arithmetic_is_exact(self):
        big = 2**80 + 12345
        batch = Batch(self.schema, {"n": [big], "f": [0.0]})
        derived = batch.derive_column(
            make_column("n2", TypeTag.INT, nullable=False),
            lambda row: row["n"] + row["n"],
        )
        result = derived.column_values("n2")[0]
        self.assertEqual(result, 2 * big)
        self.assertIsInstance(result, int)

    def test_type_validation_reports_row_and_column(self):
        with self.assertRaises(BatchError) as ctx:
            Batch(self.schema, {"n": [1, "oops"], "f": [0.0, 1.0]})
        message = str(ctx.exception)
        self.assertIn("row 1", message)
        self.assertIn("'n'", message)

    def test_null_in_non_nullable_column_rejected(self):
        with self.assertRaises(BatchError):
            Batch(self.schema, {"n": [NULL], "f": [1.0]})

    def test_column_set_mismatch_rejected(self):
        with self.assertRaises(BatchError):
            Batch(self.schema, {"n": [1]})

    def test_ragged_columns_rejected(self):
        with self.assertRaises(BatchError):
            Batch(self.schema, {"n": [1, 2], "f": [1.0]})

    def test_bool_is_not_an_int(self):
        with self.assertRaises(BatchError):
            Batch(self.schema, {"n": [True], "f": [1.0]})


if __name__ == "__main__":
    unittest.main()
