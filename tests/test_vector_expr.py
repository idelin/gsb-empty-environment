"""Unit tests for the vector expression engine (stdlib unittest only)."""

import math
import unittest

from vector_expr import (
    INT64_MAX,
    INT64_MIN,
    Batch,
    CastError,
    Column,
    CompileError,
    DivisionByZeroError,
    EvaluationError,
    IntegerOverflowError,
    Type,
    compile_expression,
)


def run(expr, schema=None, columns=None):
    """Compile ``expr`` and execute it over a one-shot batch."""
    schema = schema or {}
    plan = compile_expression(expr, schema)
    batch = Batch({name: Column(schema[name], vals)
                   for name, vals in (columns or {}).items()})
    return plan.execute(batch).values


class TestArithmetic(unittest.TestCase):
    def test_integer_arithmetic_is_exact(self):
        cols = {"a": [INT64_MAX - 1, 7, -7]}
        self.assertEqual(run("a + 1", {"a": Type.INT64}, cols),
                         [INT64_MAX, 8, -6])
        self.assertEqual(run("a - a", {"a": Type.INT64}, cols), [0, 0, 0])
        # exact big multiplication just inside the range
        self.assertEqual(
            run("a * 2", {"a": Type.INT64}, {"a": [2**62 - 1]}),
            [2**63 - 2],
        )

    def test_integer_division_truncates_toward_zero(self):
        cols = {"a": [7, -7, 7, -7], "b": [2, 2, -2, -2]}
        schema = {"a": Type.INT64, "b": Type.INT64}
        self.assertEqual(run("a / b", schema, cols), [3, -3, -3, 3])
        self.assertEqual(run("a % b", schema, cols), [1, -1, 1, -1])

    def test_float_arithmetic(self):
        out = run("a * 2.0 + 0.5", {"a": Type.FLOAT64}, {"a": [1.0, 2.5]})
        self.assertEqual(out, [2.5, 5.5])

    def test_float_division_by_zero_is_ieee(self):
        out = run("a / 0.0", {"a": Type.FLOAT64}, {"a": [1.0, -1.0, 0.0]})
        self.assertEqual(out[0], math.inf)
        self.assertEqual(out[1], -math.inf)
        self.assertTrue(math.isnan(out[2]))

    def test_integer_overflow_reports_row(self):
        cols = {"a": [1, INT64_MAX, 5]}
        with self.assertRaises(IntegerOverflowError) as ctx:
            run("a + 1", {"a": Type.INT64}, cols)
        self.assertEqual(ctx.exception.row, 1)
        self.assertIn("row 1", str(ctx.exception))

    def test_integer_multiply_overflow_reports_row(self):
        cols = {"a": [2, 2**62]}
        with self.assertRaises(IntegerOverflowError) as ctx:
            run("a * 2", {"a": Type.INT64}, cols)
        self.assertEqual(ctx.exception.row, 1)

    def test_int64_min_divided_by_minus_one_overflows(self):
        with self.assertRaises(IntegerOverflowError) as ctx:
            run("a / -1", {"a": Type.INT64}, {"a": [INT64_MIN]})
        self.assertEqual(ctx.exception.row, 0)

    def test_integer_division_by_zero_reports_row(self):
        cols = {"a": [10, 20], "b": [2, 0]}
        schema = {"a": Type.INT64, "b": Type.INT64}
        with self.assertRaises(DivisionByZeroError) as ctx:
            run("a / b", schema, cols)
        self.assertEqual(ctx.exception.row, 1)

    def test_null_propagates_through_arithmetic(self):
        cols = {"a": [1, None, 3]}
        self.assertEqual(run("a + 10", {"a": Type.INT64}, cols),
                         [11, None, 13])


class TestComparisonAndLogic(unittest.TestCase):
    def test_null_comparison_is_unknown_not_false(self):
        cols = {"a": [1, None, 3], "b": [1, 2, None]}
        schema = {"a": Type.INT64, "b": Type.INT64}
        self.assertEqual(run("a = b", schema, cols), [True, None, None])
        self.assertEqual(run("a != b", schema, cols), [False, None, None])
        self.assertEqual(run("a < b", schema, cols), [False, None, None])

    def test_comparison_operators(self):
        cols = {"a": [1, 2, 3]}
        self.assertEqual(run("a >= 2", {"a": Type.INT64}, cols),
                         [False, True, True])
        self.assertEqual(run("a > 2", {"a": Type.INT64}, cols),
                         [False, False, True])
        self.assertEqual(run("a <= 2", {"a": Type.INT64}, cols),
                         [True, True, False])

    def test_kleene_and_or_not(self):
        cols = {"p": [True, True, False, False, None, None, True, False, None],
                "q": [True, False, True, False, True, False, None, None, None]}
        schema = {"p": Type.BOOL, "q": Type.BOOL}
        self.assertEqual(run("p AND q", schema, cols),
                         [True, False, False, False, None, False, None,
                          False, None])
        self.assertEqual(run("p OR q", schema, cols),
                         [True, True, True, False, True, None, True, None,
                          None])
        self.assertEqual(run("NOT p", schema, cols),
                         [False, False, True, True, None, None, False, True,
                          None])

    def test_bool_equality(self):
        cols = {"p": [True, False]}
        self.assertEqual(run("p = true", {"p": Type.BOOL}, cols),
                         [True, False])


class TestShortCircuitIf(unittest.TestCase):
    def test_if_false_literal_skips_division_by_zero(self):
        # the classic: unselected branch must not blow up
        plan = compile_expression("if(false, 1/0, 7)", {})
        batch = Batch({"dummy": Column(Type.INT64, [0, 0, 0])})
        self.assertEqual(plan.execute(batch).values, [7, 7, 7])

    def test_if_true_constant(self):
        plan = compile_expression("if(true, 42, 1/0)", {})
        batch = Batch({"dummy": Column(Type.INT64, [0])})
        self.assertEqual(plan.execute(batch).values, [42])

    def test_per_row_short_circuit(self):
        # cond is true everywhere, so the else-branch 1/0 is never evaluated
        schema = {"c": Type.BOOL}
        plan = compile_expression("if(c, 10, 1/0)", schema)
        batch = Batch({"c": Column(Type.BOOL, [True, True])})
        self.assertEqual(plan.execute(batch).values, [10, 10])

    def test_per_row_short_circuit_both_sides(self):
        schema = {"c": Type.BOOL, "x": Type.INT64, "y": Type.INT64}
        plan = compile_expression("if(c, 100 / x, 200 / y)", schema)
        # row 0: c=true,  x=2 -> 50; y=0 would divide by zero but is masked
        # row 1: c=false, y=4 -> 50; x=0 would divide by zero but is masked
        batch = Batch({
            "c": Column(Type.BOOL, [True, False]),
            "x": Column(Type.INT64, [2, 0]),
            "y": Column(Type.INT64, [0, 4]),
        })
        self.assertEqual(plan.execute(batch).values, [50, 50])

    def test_selected_branch_error_still_reports_row(self):
        schema = {"c": Type.BOOL, "x": Type.INT64}
        plan = compile_expression("if(c, 1 / x, 7)", schema)
        batch = Batch({"c": Column(Type.BOOL, [False, True]),
                       "x": Column(Type.INT64, [0, 0])})
        with self.assertRaises(DivisionByZeroError) as ctx:
            plan.execute(batch)
        self.assertEqual(ctx.exception.row, 1)  # row 0 was masked out

    def test_null_condition_yields_null(self):
        schema = {"c": Type.BOOL}
        plan = compile_expression("if(c, 1, 2)", schema)
        batch = Batch({"c": Column(Type.BOOL, [None, True, False])})
        self.assertEqual(plan.execute(batch).values, [None, 1, 2])

    def test_nested_if(self):
        schema = {"a": Type.INT64}
        plan = compile_expression(
            "if(a > 0, 1, if(a < 0, -1, 0))", schema)
        batch = Batch({"a": Column(Type.INT64, [5, -3, 0, None])})
        self.assertEqual(plan.execute(batch).values, [1, -1, 0, None])


class TestCasts(unittest.TestCase):
    def test_int_to_float(self):
        out = run("CAST(a AS FLOAT64)", {"a": Type.INT64}, {"a": [1, None]})
        self.assertEqual(out, [1.0, None])
        self.assertIsInstance(out[0], float)

    def test_float_to_int_truncates_toward_zero(self):
        out = run("CAST(a AS INT64)", {"a": Type.FLOAT64},
                  {"a": [2.7, -2.7, 3.0]})
        self.assertEqual(out, [2, -2, 3])

    def test_cast_nan_to_int_reports_row(self):
        with self.assertRaises(CastError) as ctx:
            run("CAST(a AS INT64)", {"a": Type.FLOAT64},
                {"a": [1.0, math.nan]})
        self.assertEqual(ctx.exception.row, 1)

    def test_cast_out_of_range_reports_row(self):
        with self.assertRaises(IntegerOverflowError) as ctx:
            run("CAST(a AS INT64)", {"a": Type.FLOAT64}, {"a": [1e300]})
        self.assertEqual(ctx.exception.row, 0)

    def test_cast_enables_mixed_arithmetic(self):
        schema = {"a": Type.INT64, "b": Type.FLOAT64}
        cols = {"a": [1, 2], "b": [0.5, 0.25]}
        out = run("CAST(a AS FLOAT64) + b", schema, cols)
        self.assertEqual(out, [1.5, 2.25])

    def test_typed_null_literal(self):
        plan = compile_expression(
            "if(false, CAST(NULL AS INT64), 5)", {})
        batch = Batch({"d": Column(Type.INT64, [0])})
        self.assertEqual(plan.execute(batch).values, [5])


class TestNullVersusNaN(unittest.TestCase):
    def test_nan_is_not_null(self):
        nan = float("nan")
        cols = {"x": [nan, None, 1.0]}
        # null propagates to null; NaN is a value and NaN = NaN is false
        out = run("x = x", {"x": Type.FLOAT64}, cols)
        self.assertEqual(out, [False, None, True])

    def test_nan_arithmetic_stays_value(self):
        nan = float("nan")
        out = run("x + 1.0", {"x": Type.FLOAT64}, {"x": [nan, None]})
        self.assertTrue(math.isnan(out[0]))
        self.assertIsNone(out[1])

    def test_nan_not_equal(self):
        nan = float("nan")
        out = run("x != x", {"x": Type.FLOAT64}, {"x": [nan]})
        self.assertEqual(out, [True])


class TestCompileTimeErrors(unittest.TestCase):
    """All of these must fail before any batch exists."""

    def test_arithmetic_on_bool(self):
        with self.assertRaises(CompileError):
            compile_expression("1 + true", {})

    def test_mixed_numeric_types_rejected(self):
        with self.assertRaises(CompileError) as ctx:
            compile_expression("a + 1.5", {"a": Type.INT64})
        self.assertIn("CAST", str(ctx.exception))

    def test_if_condition_must_be_bool(self):
        with self.assertRaises(CompileError):
            compile_expression("if(1, 2, 3)", {})

    def test_if_branch_type_mismatch(self):
        with self.assertRaises(CompileError):
            compile_expression("if(true, 1, 2.0)", {})

    def test_unknown_column(self):
        with self.assertRaises(CompileError) as ctx:
            compile_expression("nope + 1", {"a": Type.INT64})
        self.assertIn("nope", str(ctx.exception))

    def test_and_requires_bool(self):
        with self.assertRaises(CompileError):
            compile_expression("a AND true", {"a": Type.INT64})

    def test_ordering_on_bool_rejected(self):
        with self.assertRaises(CompileError):
            compile_expression("a < b", {"a": Type.BOOL, "b": Type.BOOL})

    def test_compare_mixed_types_rejected(self):
        with self.assertRaises(CompileError):
            compile_expression("a = 1.0", {"a": Type.INT64})

    def test_syntax_error(self):
        with self.assertRaises(CompileError):
            compile_expression("1 +", {})

    def test_unknown_cast_target(self):
        with self.assertRaises(CompileError):
            compile_expression("CAST(1 AS TEXT)", {})

    def test_type_error_points_at_source(self):
        with self.assertRaises(CompileError) as ctx:
            compile_expression("a + true", {"a": Type.INT64})
        self.assertIn("^", str(ctx.exception))


class TestBatchValidation(unittest.TestCase):
    def test_missing_column_at_execution(self):
        plan = compile_expression("a + 1", {"a": Type.INT64})
        with self.assertRaises(EvaluationError):
            plan.execute(Batch({"b": Column(Type.INT64, [1])}))

    def test_wrong_column_type_at_execution(self):
        plan = compile_expression("a + 1", {"a": Type.INT64})
        with self.assertRaises(EvaluationError):
            plan.execute(Batch({"a": Column(Type.FLOAT64, [1.0])}))

    def test_column_value_validation(self):
        with self.assertRaises(ValueError):
            Column(Type.INT64, [1.5])
        with self.assertRaises(ValueError):
            Column(Type.INT64, [True])
        with self.assertRaises(ValueError):
            Column(Type.INT64, [2**63])
        with self.assertRaises(ValueError):
            Column(Type.BOOL, [1])

    def test_unequal_column_lengths(self):
        with self.assertRaises(ValueError):
            Batch({"a": Column(Type.INT64, [1]),
                   "b": Column(Type.INT64, [1, 2])})

    def test_plan_reusable_across_batches(self):
        plan = compile_expression("a * 2", {"a": Type.INT64})
        b1 = Batch({"a": Column(Type.INT64, [1, 2])})
        b2 = Batch({"a": Column(Type.INT64, [3])})
        self.assertEqual(plan.execute(b1).values, [2, 4])
        self.assertEqual(plan.execute(b2).values, [6])
        self.assertEqual(plan.result_type, Type.INT64)


if __name__ == "__main__":
    unittest.main()
