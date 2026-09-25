import math
import unittest

from vecplan import (BOOL, FLOAT64, INT32, INT64, STRING, CompileError,
                     ErrorPolicy, ExecutionError, RecordBatch, and_, cast,
                     col, compile, if_, is_null, lit, not_, or_)


def run(expr, batch, policy=ErrorPolicy.STRICT):
    return compile(expr, batch.schema).run(batch, policy).values


class CompileTimeTypeErrors(unittest.TestCase):
    def setUp(self):
        self.schema = {"i": INT32, "f": FLOAT64, "s": STRING, "flag": BOOL}

    def test_arithmetic_on_string_rejected(self):
        with self.assertRaises(CompileError):
            compile(col("s") + 1, self.schema)

    def test_if_condition_must_be_bool(self):
        with self.assertRaises(CompileError):
            compile(if_(col("i"), 1, 2), self.schema)

    def test_if_branches_must_be_compatible(self):
        with self.assertRaises(CompileError):
            compile(if_(col("flag"), col("i"), col("s")), self.schema)

    def test_incompatible_comparison_rejected(self):
        with self.assertRaises(CompileError):
            compile(col("s") < col("i"), self.schema)

    def test_mod_requires_integers(self):
        with self.assertRaises(CompileError):
            compile(col("f") % 2, self.schema)

    def test_unknown_column_rejected(self):
        with self.assertRaises(CompileError):
            compile(col("nope") + 1, self.schema)

    def test_unknown_cast_target_rejected(self):
        with self.assertRaises(CompileError):
            compile(cast(col("i"), "DATE"), self.schema)

    def test_untyped_null_literal_rejected(self):
        with self.assertRaises(CompileError):
            compile(lit(None), self.schema)

    def test_oversized_literal_rejected(self):
        with self.assertRaises(CompileError):
            compile(lit(2 ** 40, INT32), self.schema)

    def test_type_errors_surface_before_execution(self):
        # The bad branch would never run, yet the error is reported at
        # compile time, before any batch exists.
        with self.assertRaises(CompileError):
            compile(if_(lit(False), col("s") + 1, lit(7)), {"s": STRING})


class ShortCircuit(unittest.TestCase):
    def test_if_false_literal_returns_else_branch(self):
        batch = RecordBatch({"x": INT32}, {"x": [1, 2, 3]})
        self.assertEqual(run(if_(lit(False), lit(1) / lit(0), lit(7)), batch),
                         [7, 7, 7])

    def test_unselected_branch_not_evaluated(self):
        batch = RecordBatch({"a": INT32, "b": INT32},
                            {"a": [10, 20, 30], "b": [2, 0, 5]})
        result = run(if_(col("b") != 0, col("a") / col("b"), lit(-1)), batch)
        self.assertEqual(result, [5, -1, 6])

    def test_selected_branch_division_by_zero_reports_rows(self):
        batch = RecordBatch({"a": INT32, "b": INT32},
                            {"a": [10, 20, 30], "b": [0, 2, 0]})
        plan = compile(if_(col("a") > 15, col("a") / col("b"), lit(0)),
                       batch.schema)
        with self.assertRaises(ExecutionError) as ctx:
            plan.run(batch)
        self.assertEqual(ctx.exception.rows, [2])
        self.assertIn("division by zero at row(s) [2]", str(ctx.exception))

    def test_nested_if_short_circuits(self):
        batch = RecordBatch({"a": INT32}, {"a": [1, 2, 3]})
        expr = if_(col("a") > 1,
                   if_(col("a") > 5, lit(1) / lit(0), lit(5)),
                   lit(7))
        self.assertEqual(run(expr, batch), [7, 5, 5])

    def test_null_condition_yields_null_without_running_branches(self):
        batch = RecordBatch({"a": INT32, "b": INT32},
                            {"a": [None], "b": [0]})
        expr = if_(col("a") > 0, col("a") / col("b"), lit(-1))
        self.assertEqual(run(expr, batch), [None])


class OverflowPolicy(unittest.TestCase):
    def test_strict_reports_exact_rows(self):
        batch = RecordBatch({"a": INT32}, {"a": [2 ** 31 - 1, 5, -(2 ** 31)]})
        plan = compile(col("a") + col("a"), batch.schema)
        with self.assertRaises(ExecutionError) as ctx:
            plan.run(batch, ErrorPolicy.STRICT)
        self.assertEqual(ctx.exception.rows, [0, 2])
        self.assertIn("integer overflow at row(s) [0, 2]", str(ctx.exception))

    def test_null_policy_yields_nulls(self):
        batch = RecordBatch({"a": INT32}, {"a": [2 ** 31 - 1, 5]})
        self.assertEqual(run(col("a") + col("a"), batch, ErrorPolicy.NULL),
                         [None, 10])

    def test_multiplication_overflow(self):
        batch = RecordBatch({"a": INT32}, {"a": [2 ** 20, 3]})
        self.assertEqual(run(col("a") * col("a"), batch, ErrorPolicy.NULL),
                         [None, 9])

    def test_integer_arithmetic_is_exact(self):
        batch = RecordBatch({"x": INT64}, {"x": [2 ** 53 + 1]})
        self.assertEqual(run(col("x") + 1, batch), [2 ** 53 + 2])
        self.assertEqual(run(col("x") * 4, batch), [(2 ** 53 + 1) * 4])

    def test_integer_division_truncates_toward_zero(self):
        batch = RecordBatch({"a": INT32, "b": INT32},
                            {"a": [-7, 7], "b": [2, -2]})
        self.assertEqual(run(col("a") / col("b"), batch), [-3, -3])
        self.assertEqual(run(col("a") % col("b"), batch), [-1, 1])

    def test_division_by_zero_null_policy(self):
        batch = RecordBatch({"a": INT32, "b": INT32},
                            {"a": [6, 7], "b": [0, 2]})
        self.assertEqual(run(col("a") / col("b"), batch, ErrorPolicy.NULL),
                         [None, 3])


class NullSemantics(unittest.TestCase):
    def setUp(self):
        self.batch = RecordBatch(
            {"i": INT32, "f": FLOAT64},
            {"i": [1, None, 3], "f": [1.0, float("nan"), None]})

    def test_null_comparison_is_unknown_not_false(self):
        self.assertEqual(run(col("i") == 1, self.batch), [True, None, False])
        self.assertEqual(run(col("i") != 1, self.batch), [False, None, True])
        self.assertEqual(run(col("i") < 2, self.batch), [True, None, False])

    def test_null_propagates_through_arithmetic(self):
        self.assertEqual(run(col("i") + 1, self.batch), [2, None, 4])

    def test_not_of_null_is_null(self):
        self.assertEqual(run(not_(col("i") == 1), self.batch),
                         [False, None, True])

    def test_three_valued_logic(self):
        batch = RecordBatch(
            {"p": BOOL, "q": BOOL},
            {"p": [True, True, True, False, False, False, None, None, None],
             "q": [True, False, None, True, False, None, True, False, None]})
        self.assertEqual(run(and_(col("p"), col("q")), batch),
                         [True, False, None, False, False, False,
                          None, False, None])
        self.assertEqual(run(or_(col("p"), col("q")), batch),
                         [True, True, True, True, False, None,
                          True, None, None])

    def test_nan_is_not_null(self):
        self.assertEqual(run(is_null(col("f")), self.batch),
                         [False, False, True])

    def test_nan_compares_with_ieee_semantics(self):
        self.assertEqual(run(col("f") == col("f"), self.batch),
                         [True, False, None])
        self.assertEqual(run(col("f") != col("f"), self.batch),
                         [False, True, None])


class Casts(unittest.TestCase):
    def setUp(self):
        self.batch = RecordBatch(
            {"i": INT32, "f": FLOAT64, "s": STRING},
            {"i": [42, None, -7],
             "f": [3.9, -3.9, float("nan")],
             "s": ["17", "oops", None]})

    def test_float_to_int_truncates_toward_zero(self):
        plan = compile(cast(col("f"), INT32), self.batch.schema)
        self.assertEqual(plan.run(self.batch, ErrorPolicy.NULL).values,
                         [3, -3, None])

    def test_int_to_float(self):
        self.assertEqual(run(cast(col("i"), FLOAT64), self.batch),
                         [42.0, None, -7.0])

    def test_string_to_int_with_bad_rows(self):
        plan = compile(cast(col("s"), INT32), self.batch.schema)
        with self.assertRaises(ExecutionError) as ctx:
            plan.run(self.batch, ErrorPolicy.STRICT)
        self.assertEqual(ctx.exception.rows, [1])
        self.assertEqual(plan.run(self.batch, ErrorPolicy.NULL).values,
                         [17, None, None])

    def test_cast_overflow_reports_rows(self):
        batch = RecordBatch({"big": INT64}, {"big": [2 ** 40, 5]})
        plan = compile(cast(col("big"), INT32), batch.schema)
        with self.assertRaises(ExecutionError) as ctx:
            plan.run(batch)
        self.assertEqual(ctx.exception.rows, [0])

    def test_nan_to_int_is_an_error(self):
        plan = compile(cast(col("f"), INT64), self.batch.schema)
        with self.assertRaises(ExecutionError) as ctx:
            plan.run(self.batch)
        self.assertEqual(ctx.exception.rows, [2])

    def test_int_to_string(self):
        self.assertEqual(run(cast(col("i"), STRING), self.batch),
                         ["42", None, "-7"])

    def test_identity_cast(self):
        self.assertEqual(run(cast(col("i"), INT32), self.batch),
                         [42, None, -7])


class BatchValidation(unittest.TestCase):
    def test_column_type_mismatch_rejected(self):
        with self.assertRaises(TypeError):
            RecordBatch({"a": INT32}, {"a": ["nope"]})

    def test_bool_is_not_an_int(self):
        with self.assertRaises(TypeError):
            RecordBatch({"a": INT32}, {"a": [True]})

    def test_out_of_range_input_rejected(self):
        with self.assertRaises(ValueError):
            RecordBatch({"a": INT32}, {"a": [2 ** 31]})

    def test_ragged_columns_rejected(self):
        with self.assertRaises(ValueError):
            RecordBatch({"a": INT32, "b": INT32}, {"a": [1], "b": [1, 2]})

    def test_nan_and_none_are_both_valid_float_cells(self):
        batch = RecordBatch({"f": FLOAT64},
                            {"f": [float("nan"), None, 1.5]})
        self.assertTrue(math.isnan(batch.columns["f"][0]))
        self.assertIsNone(batch.columns["f"][1])


class EndToEnd(unittest.TestCase):
    def test_vectorized_query(self):
        batch = RecordBatch(
            {"price": INT32, "qty": INT32, "tag": STRING},
            {"price": [100, 200, None, 50],
             "qty": [2, 0, 3, 4],
             "tag": ["a", "b", "c", "d"]})
        expr = if_((col("qty") > 0) & (col("price") > 75),
                   col("price") * col("qty"),
                   lit(0))
        self.assertEqual(run(expr, batch), [200, 0, None, 0])


if __name__ == "__main__":
    unittest.main()
