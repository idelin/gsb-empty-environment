import random
import unittest

import pure_expr as pe


def outcome(source, variables=None):
    try:
        return ("value", pe.evaluate(pe.parse(source), variables))
    except pe.EvalError as exc:
        return ("error", exc.kind, exc.span)
    except pe.ParseError as exc:
        return ("parse_error", exc.message, exc.span)


def optimized_outcome(result, variables=None):
    try:
        return ("value", pe.evaluate(result["optimized_ast"], variables))
    except pe.EvalError as exc:
        return ("error", exc.kind, exc.span)


class ParserAndEvaluatorTests(unittest.TestCase):
    def test_utf8_source_positions(self):
        source = "值 + 1"
        node = pe.parse(source)
        self.assertEqual(node.left.name, "值")
        self.assertEqual(node.left.span, (0, 3))
        self.assertEqual(node.op_span, (4, 5))
        self.assertEqual(pe.source_slice(source, node.left.span), "值")
        self.assertEqual(pe.evaluate(node, {"值": 41}), 42)

    def test_precedence_and_short_circuit(self):
        variables = {"calls": 0}
        source = "true || 1 == 2 && false ? 40 + 2 : 0"
        self.assertEqual(pe.evaluate(pe.parse(source), variables), 42)

    def test_non_boolean_condition_is_positioned_at_condition(self):
        result = outcome("1 ? 2 : 3")
        self.assertEqual(result, ("error", "TypeError", (0, 1)))

    def test_parse_error_uses_byte_span(self):
        result = outcome("值 @ 1")
        self.assertEqual(result[0], "parse_error")
        self.assertEqual(result[2], (4, 5))


class ConstantFoldingTests(unittest.TestCase):
    def test_folds_arithmetic_bottom_up(self):
        result = pe.optimize("1 + 2 * 3")
        self.assertEqual(result["optimized"], "7")
        self.assertEqual(pe.evaluate(result["optimized_ast"]), 7)
        self.assertEqual(result["reasons"]["by_kind"]["constant_fold"], ["cf-1", "cf-2"])
        self.assertEqual(result["reasons"]["by_id"]["cf-1"]["span"], [4, 9])

    def test_division_by_zero_keeps_operator_byte_span(self):
        source = "1 / 0"
        before = outcome(source)
        optimized = pe.optimize(source)["optimized_ast"]
        with self.assertRaises(pe.EvalError) as caught:
            pe.evaluate(optimized)
        self.assertEqual(before, ("error", "DivisionByZero", (2, 3)))
        self.assertEqual(caught.exception.span, (2, 3))

    def test_false_and_possible_error_becomes_false(self):
        result = pe.optimize("false && (1 / 0)")
        self.assertEqual(result["optimized"], "false")
        self.assertEqual(pe.evaluate(result["optimized_ast"]), False)
        reason = result["reasons"]["by_id"]["bs-1"]
        self.assertEqual(reason["removed_span"], [9, 16])

    def test_true_or_possible_error_becomes_true(self):
        result = pe.optimize("true || (1 / 0)")
        self.assertEqual(result["optimized"], "true")
        self.assertEqual(pe.evaluate(result["optimized_ast"]), True)

    def test_erroring_operands_are_not_reordered(self):
        source = "(a / 0) + (b / 0)"
        result = pe.optimize(source)
        self.assertIn("a / 0", result["optimized"])
        self.assertIn("b / 0", result["optimized"])
        self.assertLess(result["optimized_ast"].left.span[0], result["optimized_ast"].right.span[0])
        self.assertEqual(outcome(source), optimized_outcome(result))

    def test_non_commutative_constant_folding_preserves_order(self):
        self.assertEqual(pe.evaluate(pe.optimize("10 - 3 * 2")["optimized_ast"]), 4)
        self.assertEqual(pe.evaluate(pe.optimize("17 % 5")["optimized_ast"]), 2)

    def test_runtime_type_error_prefers_left_operand(self):
        source = "true + 1"
        result = pe.optimize(source)
        self.assertEqual(outcome(source), optimized_outcome(result))
        self.assertEqual(outcome(source)[2], (0, 4))

    def test_runtime_undefined_error_prefers_left_operand(self):
        source = "missing + missing"
        result = pe.optimize(source)
        self.assertEqual(outcome(source), optimized_outcome(result))
        self.assertEqual(outcome(source)[2], (0, 7))


class BranchTests(unittest.TestCase):
    def test_constant_true_selects_then_and_preserves_error(self):
        source = "true ? 1 / 0 : 2 / 0"
        result = pe.optimize(source)
        self.assertIn("DivisionByZero", result["optimized"])
        self.assertNotIn("2 / 0", result["optimized"])
        self.assertEqual(outcome(source), optimized_outcome(result))
        self.assertEqual(result["reasons"]["by_id"]["bs-1"]["selected_span"], [7, 12])

    def test_constant_false_removes_erroring_then_branch(self):
        source = "false ? 1 / 0 : 7"
        result = pe.optimize(source)
        self.assertEqual(result["optimized"], "7")
        self.assertEqual(pe.evaluate(result["optimized_ast"]), 7)

    def test_non_boolean_condition_contract_matches(self):
        source = "1 ? 2 : 3"
        result = pe.optimize(source)
        self.assertEqual(outcome(source), optimized_outcome(result))


class CommonSubexpressionTests(unittest.TestCase):
    def test_eliminates_repeated_pure_expression(self):
        source = "(x + 1) + (x + 1)"
        result = pe.optimize(source)
        self.assertIn("@cse1(x + 1)", result["optimized"])
        self.assertIn("cse1", result["optimized"])
        self.assertEqual(pe.evaluate(result["optimized_ast"], {"x": 40}), 82)
        ids = result["reasons"]["by_kind"]["common_subexpression_elimination"]
        self.assertEqual(len(ids), 2)
        self.assertEqual(result["reasons"]["by_span"]["0:7"], ["cse-1"])
        self.assertEqual(result["reasons"]["by_span"]["10:17"], ["cse-2"])

    def test_repeated_possible_error_keeps_first_error_position(self):
        source = "(x / 0) + (x / 0)"
        result = pe.optimize(source)
        self.assertEqual(pe.render(result["optimized_ast"].left), "@cse1(x / 0)")
        self.assertIsInstance(result["optimized_ast"].right, pe.Use)
        self.assertEqual(outcome(source, {"x": 1}), optimized_outcome(result, {"x": 1}))

    def test_left_to_right_order_is_preserved(self):
        source = "((x + 1) + (y + 1)) + (x + 1)"
        result = pe.optimize(source)
        optimized = result["optimized"]
        self.assertIn("@cse1(x + 1) + (y + 1)", optimized)
        variables = {"x": 1, "y": 10}
        self.assertEqual(pe.evaluate(result["optimized_ast"], variables), 15)

    def test_bindings_do_not_leak_across_conditional_branches(self):
        result = pe.optimize("c ? x + 1 : x + 1")
        self.assertEqual(result["optimized"], "c ? x + 1 : x + 1")
        self.assertEqual(result["reasons"]["by_kind"]["common_subexpression_elimination"], [])

    def test_short_circuited_binding_is_not_evaluated(self):
        source = "false && ((x + 1) + (x + 1))"
        result = pe.optimize(source)
        self.assertEqual(result["optimized"], "false")
        self.assertEqual(pe.evaluate(result["optimized_ast"], {}), False)


class EquivalenceFuzzTests(unittest.TestCase):
    def test_random_expressions_match(self):
        rng = random.Random(20260925)
        variables = {"x": 3, "y": 5, "z": 7, "p": True, "q": False}

        def generate(depth):
            if depth == 0 or rng.random() < 0.22:
                return rng.choice(["0", "1", "2", "3", "true", "false", "x", "y", "z", "p", "q"])
            kind = rng.choice(["+", "-", "*", "==", "<", "&&", "||", "?:", "!"])
            if kind == "?:":
                return f"({generate(depth - 1)} ? {generate(depth - 1)} : {generate(depth - 1)})"
            if kind == "!":
                return f"!{generate(depth - 1)}"
            return f"({generate(depth - 1)} {kind} {generate(depth - 1)})"

        for _ in range(200):
            source = generate(4)
            optimized = pe.optimize(source)
            self.assertEqual(outcome(source, variables), optimized_outcome(optimized, variables), source)


if __name__ == "__main__":
    unittest.main()
