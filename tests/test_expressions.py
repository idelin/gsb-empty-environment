import unittest

from plan_dag import (
    BOOL,
    DocumentFormatError,
    FLOAT,
    INT,
    NULL,
    TEXT,
    Schema,
    TypeMismatchError,
    binop,
    coalesce,
    col,
    expr_from_dict,
    lit,
)
from plan_dag.expr import IsNullExpr, NotExpr

SCHEMA = Schema.of(("a", INT), ("b", INT), ("s", TEXT), ("flag", BOOL))


class ThreeValuedLogicTests(unittest.TestCase):
    def evaluate(self, expr, row):
        return expr.bind(SCHEMA, "test").eval(row)

    def test_comparison_with_null_yields_null(self):
        expr = binop("=", col("a"), lit(5))
        self.assertIs(self.evaluate(expr, (NULL, 1, "x", True)), NULL)
        self.assertIs(self.evaluate(expr, (5, 1, "x", True)), True)
        self.assertIs(self.evaluate(expr, (6, 1, "x", True)), False)

    def test_null_equality_is_null_not_true(self):
        expr = binop("=", col("a"), lit(None, INT))
        self.assertIs(self.evaluate(expr, (NULL, 1, "x", True)), NULL)

    def test_isnull_is_plain_boolean(self):
        expr = IsNullExpr(col("a"))
        self.assertIs(self.evaluate(expr, (NULL, 1, "x", True)), True)
        self.assertIs(self.evaluate(expr, (1, 1, "x", True)), False)
        self.assertIs(self.evaluate(IsNullExpr(col("a"), negated=True), (NULL, 1, "x", True)), False)

    def test_and_truth_table(self):
        cases = {
            (True, True): True,
            (True, False): False,
            (False, True): False,
            (False, False): False,
            (True, NULL): NULL,
            (NULL, True): NULL,
            (False, NULL): False,
            (NULL, False): False,
            (NULL, NULL): NULL,
        }
        for (left, right), expected in cases.items():
            with self.subTest(left=left, right=right):
                expr = binop("and", lit(left, BOOL), lit(right, BOOL))
                result = self.evaluate(expr, (1, 1, "x", True))
                if expected is NULL:
                    self.assertIs(result, NULL)
                else:
                    self.assertIs(result, expected)

    def test_or_and_not(self):
        self.assertIs(
            self.evaluate(binop("or", lit(False, BOOL), lit(NULL, BOOL)), (1, 1, "x", True)),
            NULL,
        )
        self.assertIs(
            self.evaluate(binop("or", lit(True, BOOL), lit(NULL, BOOL)), (1, 1, "x", True)),
            True,
        )
        self.assertIs(self.evaluate(NotExpr(lit(NULL, BOOL)), (1, 1, "x", True)), NULL)
        self.assertIs(self.evaluate(NotExpr(lit(True, BOOL)), (1, 1, "x", True)), False)

    def test_arithmetic_null_and_types(self):
        add = binop("+", col("a"), col("b"))
        self.assertEqual(self.evaluate(add, (2, 3, "x", True)), 5)
        self.assertIs(self.evaluate(add, (NULL, 3, "x", True)), NULL)
        divide = binop("/", col("a"), col("b"))
        self.assertEqual(divide.bind(SCHEMA, "t").output_type, FLOAT)
        self.assertEqual(self.evaluate(divide, (6, 4, "x", True)), 1.5)

    def test_coalesce(self):
        expr = coalesce(col("a"), col("b"), lit(0))
        self.assertEqual(self.evaluate(expr, (NULL, NULL, "x", True)), 0)
        self.assertEqual(self.evaluate(expr, (NULL, 9, "x", True)), 9)

    def test_cross_type_comparison_rejected_before_execution(self):
        with self.assertRaises(TypeMismatchError):
            binop("=", col("a"), col("s")).bind(SCHEMA, "t")

    def test_logical_on_non_boolean_rejected(self):
        with self.assertRaises(TypeMismatchError):
            binop("and", col("a"), col("flag")).bind(SCHEMA, "t")

    def test_arithmetic_text_rejected(self):
        with self.assertRaises(TypeMismatchError):
            binop("+", col("a"), col("s")).bind(SCHEMA, "t")

    def test_ordering_booleans_rejected(self):
        with self.assertRaises(TypeMismatchError):
            binop("<", col("flag"), lit(True, BOOL)).bind(SCHEMA, "t")

    def test_unknown_column_binding_error(self):
        from plan_dag.errors import BindingError

        with self.assertRaises(BindingError):
            col("missing").bind(SCHEMA, "t")

    def test_null_literal_requires_type(self):
        with self.assertRaises(TypeMismatchError):
            lit(None)

    def test_literal_type_mismatch(self):
        with self.assertRaises(TypeMismatchError):
            lit("x", INT).bind(SCHEMA, "t")


class ExpressionDecodingTests(unittest.TestCase):
    def test_roundtrip(self):
        expr = binop("+", col("a"), coalesce(lit(1), lit(2)))
        decoded = expr_from_dict(expr.to_dict())
        self.assertEqual(decoded.to_dict(), expr.to_dict())

    def test_unknown_expression_tag_rejected(self):
        with self.assertRaises(DocumentFormatError):
            expr_from_dict({"e": "lookup_secret", "name": "x"})

    def test_unknown_binary_operator_rejected(self):
        with self.assertRaises(DocumentFormatError):
            expr_from_dict({"e": "bin", "op": "^", "left": {"e": "col", "name": "a"},
                            "right": {"e": "lit", "value": 1, "type": INT}})

    def test_unknown_fields_rejected(self):
        with self.assertRaises(DocumentFormatError):
            expr_from_dict({"e": "col", "name": "a", "inject": True})

    def test_non_object_rejected(self):
        with self.assertRaises(DocumentFormatError):
            expr_from_dict(["col", "a"])


if __name__ == "__main__":
    unittest.main()
