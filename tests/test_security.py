"""Security properties: budgets, limits, sanitization, capabilities."""

import os
import unittest

from expr_sandbox import (Budget, BudgetExhausted, Executor, ExprError,
                          FunctionRegistry, Limits, ParseError)


class TestBudget(unittest.TestCase):
    def test_step_budget_covers_evaluation(self):
        ex = Executor()
        with self.assertRaises(BudgetExhausted):
            ex.execute("1 + 2 + 3 + 4", budget=Budget(3))

    def test_budget_covers_parsing(self):
        ex = Executor()
        with self.assertRaises(BudgetExhausted):
            ex.execute("1 + 2 + 3", budget=Budget(2))

    def test_budget_is_atomic(self):
        budget = Budget(10)
        with self.assertRaises(BudgetExhausted):
            budget.consume(11, "test")
        self.assertEqual(budget.used, 0)  # nothing consumed on failure

    def test_repeat_hits_budget_before_allocation(self):
        # string would fit in max_string, but not in the step budget
        ex = Executor(limits=Limits(max_string=10 ** 9, max_steps=10 ** 9))
        with self.assertRaises(BudgetExhausted):
            ex.execute('repeat("ab", 1000000)', budget=Budget(100))

    def test_range_hits_budget_before_allocation(self):
        ex = Executor(limits=Limits(max_collection=10 ** 9,
                                    max_steps=10 ** 9))
        with self.assertRaises(BudgetExhausted):
            ex.execute("range(1000000)", budget=Budget(100))

    def test_exhaustion_is_not_treated_as_false(self):
        # Budget errors must propagate through lazy operators, never
        # silently become a falsy value.
        ex = Executor(limits=Limits(max_steps=10 ** 6))
        for src in ('true and repeat("x", 5000)',
                    'false or repeat("x", 5000)',
                    'if(true, repeat("x", 5000), 1)',
                    'not (repeat("x", 5000) == "")'):
            with self.assertRaises(BudgetExhausted, msg=src):
                ex.execute(src, budget=Budget(50))

    def test_contains_budget_not_false(self):
        ex = Executor()
        big = "a" * 500
        with self.assertRaises(BudgetExhausted):
            ex.execute('contains("%s", "b")' % big, budget=Budget(100))

    def test_shared_budget_across_constructs(self):
        # parsing + evaluation draw from one budget
        ex = Executor()
        result = ex.execute("1 + 2", budget=Budget(1000))
        self.assertGreater(result.parse_consumed, 0)
        self.assertGreater(result.eval_consumed, 0)
        self.assertEqual(result.consumed,
                         result.parse_consumed + result.eval_consumed)


class TestSizeLimits(unittest.TestCase):
    def test_string_limit_before_allocation(self):
        ex = Executor()
        with self.assertRaises(ExprError) as cm:
            ex.execute('repeat("ab", 1000000000)')
        self.assertEqual(cm.exception.message, "string too long")

    def test_exponential_concat_hits_limit(self):
        # doubling concat: 2, 4, 8, ... exceeds max_string quickly
        ex = Executor(limits=Limits(max_string=100, max_steps=10 ** 6))
        expr = '"ab"'
        for _ in range(10):
            expr = "concat(%s, %s)" % (expr, expr)
        with self.assertRaises(ExprError) as cm:
            ex.execute(expr)
        self.assertEqual(cm.exception.message, "string too long")

    def test_nested_repeat_hits_limit(self):
        ex = Executor()
        with self.assertRaises(ExprError) as cm:
            ex.execute('repeat(repeat("x", 5000), 3)')
        self.assertEqual(cm.exception.message, "string too long")

    def test_collection_limit(self):
        ex = Executor()
        with self.assertRaises(ExprError) as cm:
            ex.execute("range(20000)")
        self.assertEqual(cm.exception.message, "collection too large")
        with self.assertRaises(ExprError):
            ex.execute("range(6000) + range(6000)")

    def test_number_out_of_range(self):
        ex = Executor()
        for src in ("2 ** 62",):  # ** is not even valid syntax
            with self.assertRaises(ParseError):
                ex.execute(src)
        with self.assertRaises(ExprError) as cm:
            ex.execute(str(2 ** 62 + 1))
        self.assertEqual(cm.exception.message, "number out of range")
        with self.assertRaises(ExprError):
            ex.execute("%d * 2" % (2 ** 61 + 1))
        with self.assertRaises(ExprError):
            ex.execute("1e308 * 10")

    def test_source_size_limit(self):
        ex = Executor(limits=Limits(max_source=10))
        with self.assertRaises(ParseError):
            ex.execute("1 + " * 10 + "1")


class TestDepth(unittest.TestCase):
    def test_deep_parens_rejected_by_default(self):
        src = "(" * 5000 + "1" + ")" * 5000
        with self.assertRaises(ParseError) as cm:
            Executor().execute(src)
        self.assertEqual(cm.exception.message, "nesting too deep")

    def test_deep_lists_rejected_by_default(self):
        src = "[" * 500 + "]" * 500
        with self.assertRaises(ParseError) as cm:
            Executor().execute(src)
        self.assertEqual(cm.exception.message, "nesting too deep")

    def test_deep_unary_chain_rejected_at_eval(self):
        src = "-" * 5000 + "1"
        with self.assertRaises(ExprError) as cm:
            Executor().execute(src)
        self.assertEqual(cm.exception.message, "expression too deep")

    def test_deep_nesting_does_not_crash_host_stack(self):
        # With a raised depth limit the iterative parser/evaluator handle
        # nesting far beyond the host recursion limit (default 1000).
        limits = Limits(max_depth=20000, max_steps=10 ** 7,
                        max_source=10 ** 6)
        ex = Executor(limits=limits)
        depth = 10000
        src = "(" * depth + "1" + ")" * depth
        self.assertEqual(ex.execute(src).value, 1)
        src = "-" * 5000 + "1"
        self.assertEqual(ex.execute(src).value, 1)
        src = "[" * 5000 + "]" * 5000
        value = ex.execute(src).value
        self.assertIsInstance(value, list)

    def test_deep_value_from_host_rejected(self):
        value = []
        for _ in range(200):
            value = [value]
        ex = Executor()
        with self.assertRaises(ExprError):
            ex.execute("x", {"x": value})


class TestErrorPositionsAndSanitization(unittest.TestCase):
    def test_utf8_byte_spans(self):
        src = '  "\u03b1\u03b2" @'
        with self.assertRaises(ParseError) as cm:
            Executor().execute(src)
        err = cm.exception
        self.assertEqual(err.start, len('  "\u03b1\u03b2" '.encode("utf-8")))
        self.assertEqual(src.encode("utf-8")[err.start:err.end], b"@")

    def test_error_span_points_at_offending_expression(self):
        src = "1 + (2 / 0)"
        with self.assertRaises(ExprError) as cm:
            Executor().execute(src)
        err = cm.exception
        self.assertEqual(err.message, "division by zero")
        self.assertEqual(src.encode("utf-8")[err.start:err.end], b"2 / 0")

    def test_unknown_variable_span(self):
        src = "  abc + 1"
        with self.assertRaises(ExprError) as cm:
            Executor().execute(src)
        err = cm.exception
        self.assertEqual(src.encode("utf-8")[err.start:err.end], b"abc")

    def test_errors_do_not_leak_host_paths(self):
        workspace = os.path.abspath(".")
        bad_sources = [
            "1 / 0", "unknownvar", "nosuch(1)", "1 +", "(", '"abc',
            "len(1)", "repeat(1, 2)", "[1][0]", "1 @ 2", "if(1, 2)",
        ]
        for src in bad_sources:
            try:
                Executor().execute(src)
            except ExprError as err:
                text = str(err)
                self.assertNotIn(workspace, text)
                self.assertNotIn(os.sep + "Users", text)
                self.assertNotIn(".py", text)


class TestRegistryAndCapabilities(unittest.TestCase):
    def setUp(self):
        self.registry = FunctionRegistry()
        self.cap = self.registry.issue_capability("tests")
        self.executor = Executor(registry=self.registry)

    def test_registration_requires_valid_capability(self):
        with self.assertRaises(PermissionError):
            self.registry.register(object(), "f", lambda: 1)
        with self.assertRaises(PermissionError):
            self.registry.register(None, "f", lambda: 1)
        other = FunctionRegistry()
        foreign = other.issue_capability()
        with self.assertRaises(PermissionError):
            self.registry.register(foreign, "f", lambda: 1)

    def test_register_call_unregister(self):
        self.registry.register(self.cap, "double", lambda x: x * 2)
        self.assertEqual(self.executor.execute("double(21)").value, 42)
        self.registry.unregister(self.cap, "double")
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("double(21)")
        self.assertEqual(cm.exception.message, "unknown function 'double'")

    def test_cannot_override_builtin(self):
        with self.assertRaises(ValueError):
            self.registry.register(self.cap, "len", lambda x: 0)

    def test_unknown_function_rejected_before_evaluation(self):
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("nosuch(1)")
        self.assertEqual(cm.exception.message, "unknown function 'nosuch'")
        # leftmost unknown function is reported, even in dead code
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("1 / 0 + nosuch(2)")
        self.assertEqual(cm.exception.message, "unknown function 'nosuch'")

    def test_host_exception_is_sanitized(self):
        def boom(x):
            raise RuntimeError("secret token at /Users/alice/key.pem")

        self.registry.register(self.cap, "boom", boom)
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("boom(1)")
        text = str(cm.exception)
        self.assertEqual(cm.exception.message,
                         "function 'boom' raised an exception")
        self.assertNotIn("secret", text)
        self.assertNotIn("/Users", text)
        self.assertNotIn("key.pem", text)

    def test_host_cannot_return_host_objects(self):
        self.registry.register(self.cap, "obj", lambda: object())
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("obj()")
        self.assertIn("forbidden value", cm.exception.message)
        self.registry.register(self.cap, "me", lambda: self.executor)
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("me()")
        self.assertIn("forbidden value", cm.exception.message)
        self.registry.register(self.cap, "tup", lambda: (1, 2))
        with self.assertRaises(ExprError):
            self.executor.execute("tup()")

    def test_host_return_limits_enforced(self):
        self.registry.register(self.cap, "big", lambda: "x" * 10 ** 6)
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("big()")
        self.assertIn("string too long", cm.exception.message)
        cyclic = []
        cyclic.append(cyclic)
        self.registry.register(self.cap, "cyc", lambda: cyclic)
        with self.assertRaises(ExprError) as cm:
            self.executor.execute("cyc()")
        self.assertIn("cyclic", cm.exception.message)

    def test_executor_passes_only_plain_values(self):
        seen = []
        self.registry.register(
            self.cap, "probe",
            lambda *args: seen.append(args) or True)
        self.executor.execute('probe(1, "a", [1, [2]], true, null, 1.5)')
        self.assertEqual(seen, [(1, "a", [1, [2]], True, None, 1.5)])
        for value in seen[0]:
            self.assertIn(type(value),
                          (int, str, list, bool, type(None), float))

    def test_reentrant_callback_shares_budget(self):
        budget = Budget(100000)
        executor = self.executor

        def inc(x):
            return executor.execute("x + 1", {"x": x}, budget=budget).value

        self.registry.register(self.cap, "inc", inc)
        result = executor.execute("inc(inc(5))", budget=budget)
        self.assertEqual(result.value, 7)
        # inner (reentrant) executions drew from the same shared budget
        single = executor.execute("inc(5)", budget=Budget(100000))
        self.assertGreater(result.consumed, single.consumed)
        self.assertGreaterEqual(budget.used, result.consumed)

    def test_reentrant_exhaustion_propagates(self):
        budget = Budget(30)
        executor = self.executor

        def inc(x):
            return executor.execute("x + 1", {"x": x}, budget=budget).value

        self.registry.register(self.cap, "inc", inc)
        with self.assertRaises(BudgetExhausted):
            executor.execute("inc(inc(inc(inc(5))))", budget=budget)


if __name__ == "__main__":
    unittest.main()
