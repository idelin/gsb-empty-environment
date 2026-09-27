"""Unit tests for conservative interval analysis and exact enumeration.

All domains/expressions here are hand-constructed synthetic data.
"""

from __future__ import annotations

import io
import json
import sys
import unittest
from contextlib import redirect_stdout

from interval_check.checker import CheckStatus, analyze
from interval_check.cli import main as cli_main
from interval_check.interval import Interval
from interval_check.parser import ParseError, parse, parse_assertion


class IntervalArithmeticTests(unittest.TestCase):
    def test_addition_and_subtraction(self) -> None:
        self.assertEqual(Interval(1, 2).add(Interval(-3, 4)), Interval(-2, 6))
        self.assertEqual(Interval(1, 2).sub(Interval(-3, 4)), Interval(-3, 5))

    def test_correlation_loss_xx_is_only_a_bound(self) -> None:
        # Naive interval subtraction loses the identity x == x.
        result = analyze(
            "x - x",
            {"x": [0, 1]},
            assertion="x - x >= 0",
        )
        self.assertEqual(result.interval, [-1, 1])
        self.assertFalse(result.guaranteed)
        self.assertTrue(result.enumeration["completed"])
        self.assertEqual(
            result.status, CheckStatus.CONSERVATIVE_WARNING_REFUTED
        )
        self.assertIsNone(result.witness)
        self.assertTrue(result.findings[0].refuted)

    def test_multiplication_corners(self) -> None:
        # corner products: (-2)*(-4)=8, (-2)*5=-10, 3*(-4)=-12, 3*5=15
        self.assertEqual(
            Interval(-2, 3).mul(Interval(-4, 5)),
            Interval(-12, 15),
        )

    def test_floor_division_positive_divisor(self) -> None:
        # (-7)//3 = -3 (floor toward -infinity), (-5)//2 = -3 ...
        self.assertEqual(
            Interval(-7, -5).floor_div(Interval(2, 3)),
            Interval(-4, -2),
        )

    def test_floor_division_negative_divisor_corners(self) -> None:
        # corners: 2 // -2 = -1, (-3) // -2 = 1, 2 // -1 = -2,
        # (-3) // -1 = 3  -> [-2, 3]
        result = Interval(-3, 2).floor_div(Interval(-2, -1))
        self.assertEqual(result, Interval(-2, 3))

    def test_floor_division_by_interval_containing_zero_raises(self) -> None:
        with self.assertRaises(ZeroDivisionError):
            Interval(0, 1).floor_div(Interval(-1, 1))

    def test_empty_interval_rejected(self) -> None:
        with self.assertRaises(ValueError):
            Interval(2, 1)
        with self.assertRaises(TypeError):
            Interval(True, 3)  # type: ignore[arg-type]


class ParserTests(unittest.TestCase):
    def test_precedence_and_parentheses(self) -> None:
        tree = parse("1 + 2 * 3")
        self.assertEqual(tree.op, "+")
        self.assertEqual(tree.right.op, "*")
        tree = parse("(1 + 2) * -3")
        self.assertEqual(tree.op, "*")
        self.assertEqual(tree.left.op, "+")
        self.assertEqual(tree.right.op, "neg")

    def test_floor_div_only(self) -> None:
        tree = parse("a // b")
        self.assertEqual((tree.op, tree.left.value, tree.right.value), ("//", "a", "b"))
        with self.assertRaises(ParseError):
            parse("a / b")

    def test_reject_unknown_token(self) -> None:
        with self.assertRaises(ParseError):
            parse("x + +")
        with self.assertRaises(ParseError):
            parse("x @ 3")

    def test_assertion_parsing(self) -> None:
        assertion = parse_assertion("x - y <= -5")
        self.assertEqual((assertion.relop, assertion.rhs), ("<=", -5))
        with self.assertRaises(ParseError):
            parse_assertion("x + 1")
        with self.assertRaises(ParseError):
            parse_assertion("x > y")


class AnalysisVerdictTests(unittest.TestCase):
    def test_confirmed_assertion_counterexample(self) -> None:
        result = analyze("x + 1", {"x": [0, 2]}, assertion="x + 1 >= 2")
        self.assertEqual(result.status, CheckStatus.CONFIRMED_FAILURE)
        witness = result.witness
        self.assertIsNotNone(witness)
        self.assertEqual(witness["type"], "assertion_violation")
        self.assertEqual(witness["assignment"], {"x": 0})
        self.assertEqual(witness["expression_value"], 1)

    def test_confirmed_division_by_zero_witness(self) -> None:
        result = analyze("x // y", {"x": [0, 5], "y": [-2, 2]})
        self.assertEqual(result.status, CheckStatus.CONFIRMED_FAILURE)
        self.assertEqual(result.witness["type"], "division_by_zero")
        self.assertEqual(result.witness["assignment"]["y"], 0)
        kinds = {finding.kind for finding in result.findings}
        self.assertIn("division_by_zero_possible", kinds)
        self.assertIn("division_by_zero", kinds)

    def test_divisor_contains_zero_but_exact_domain_safe(self) -> None:
        # The divisor ((x - x) + 1) is exactly 1 for every x, but naive
        # interval arithmetic widens x - x to [-1, 1], so the divisor
        # interval [0, 2] appears to contain zero. Enumeration proves there
        # is no division by zero and refutes the conservative warning.
        result = analyze(
            "x // ((x - x) + 1)",
            {"x": [0, 1]},
            assertion="x // ((x - x) + 1) >= 0",
        )
        self.assertEqual(
            result.status, CheckStatus.CONSERVATIVE_WARNING_REFUTED
        )
        self.assertIsNone(result.witness)

    def test_interval_proves_assertion(self) -> None:
        result = analyze("x + y", {"x": [1, 3], "y": [2, 4]}, assertion="x + y >= 3")
        self.assertEqual(result.status, CheckStatus.SAFE)
        self.assertTrue(result.guaranteed)
        self.assertEqual(result.findings, [])

    def test_equality_and_inequality_guarantees(self) -> None:
        eq = analyze("x", {"x": [3, 3]}, assertion="x == 3")
        self.assertEqual(eq.status, CheckStatus.SAFE)
        ne = analyze("x", {"x": [6, 7]}, assertion="x != 5")
        self.assertEqual(ne.status, CheckStatus.SAFE)
        ne_fail = analyze("x", {"x": [4, 5]}, assertion="x != 5")
        self.assertEqual(ne_fail.status, CheckStatus.CONFIRMED_FAILURE)

    def test_negative_divisor_real_counterexample(self) -> None:
        # -1 // -1 = 1 violates "<= 0"; no division by zero occurs.
        result = analyze("x // y", {"x": [-1, 0], "y": [-1, -1]}, assertion="x // y <= 0")
        self.assertEqual(result.status, CheckStatus.CONFIRMED_FAILURE)
        self.assertEqual(result.witness["expression_value"], 1)

    def test_enumeration_skipped_above_limit_stays_conservative(self) -> None:
        result = analyze(
            "x - x",
            {"x": [0, 100]},
            assertion="x - x >= 0",
            enumerate_limit=10,
        )
        self.assertFalse(result.enumeration["performed"])
        self.assertEqual(result.status, CheckStatus.CONSERVATIVE_WARNING)
        self.assertIsNone(result.witness)

    def test_enumeration_skips_when_no_assertion_and_no_zero_divisor(self) -> None:
        result = analyze("x + 1", {"x": [0, 2]})
        self.assertIsNone(result.assertion)
        self.assertTrue(result.enumeration["completed"])
        self.assertEqual(result.status, CheckStatus.SAFE)

    def test_missing_variable_domain_reported(self) -> None:
        with self.assertRaises(ValueError):
            analyze("a + b", {"a": [0, 1]})

    def test_multi_variable_domain_product(self) -> None:
        result = analyze(
            "a * b - a",
            {"a": [0, 1], "b": [0, 1]},
            assertion="a * b - a >= 0",
        )
        # a=1, b=0 gives 0 - 1 = -1 < 0 (violation); a=b=1 gives 0 (ok).
        self.assertEqual(result.status, CheckStatus.CONFIRMED_FAILURE)
        self.assertEqual(result.witness["assignment"], {"a": 1, "b": 0})
        self.assertEqual(result.witness["expression_value"], -1)


class CliTests(unittest.TestCase):
    def _run_cli(self, payload: dict) -> tuple[int, dict]:
        raw = json.dumps(payload)
        old_stdin, old_argv = sys.stdin, sys.argv
        sys.stdin = io.StringIO(raw)
        sys.argv = ["interval_check"]
        buffer = io.StringIO()
        try:
            with redirect_stdout(buffer):
                code = cli_main([])
        finally:
            sys.stdin, sys.argv = old_stdin, old_argv
        return code, json.loads(buffer.getvalue())

    def test_acceptance_boundary_exit_code_zero(self) -> None:
        code, output = self._run_cli(
            {
                "expression": "x - x",
                "variables": {"x": [0, 1]},
                "assertion": "x - x >= 0",
            }
        )
        self.assertEqual(code, 0)
        self.assertEqual(output["status"], "conservative_warning_refuted")
        self.assertNotIn("witness", output)

    def test_confirmed_failure_exit_code_two(self) -> None:
        code, output = self._run_cli(
            {
                "expression": "x + 1",
                "variables": {"x": [0, 2]},
                "assertion": "x + 1 >= 2",
            }
        )
        self.assertEqual(code, 2)
        self.assertEqual(output["status"], "confirmed_failure")

    def test_conservative_only_exit_code_one(self) -> None:
        code, output = self._run_cli(
            {
                "expression": "x - x",
                "variables": {"x": [0, 100]},
                "assertion": "x - x >= 0",
                "enumerate_limit": 10,
            }
        )
        self.assertEqual(code, 1)
        self.assertEqual(output["status"], "conservative_warning")

    def test_bad_input_exit_code_three(self) -> None:
        old_stdin = sys.stdin
        sys.stdin = io.StringIO("{not json")
        try:
            self.assertEqual(cli_main([]), 3)
        finally:
            sys.stdin = old_stdin


if __name__ == "__main__":
    unittest.main(verbosity=2)
