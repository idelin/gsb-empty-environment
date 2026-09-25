"""Core language semantics: values, operators, laziness, builtins."""

import unittest

from expr_sandbox import Executor, ExprError, ParseError


def run(src, variables=None):
    return Executor().execute(src, variables).value


class TestArithmetic(unittest.TestCase):
    def test_precedence(self):
        self.assertEqual(run("1 + 2 * 3"), 7)
        self.assertEqual(run("(1 + 2) * 3"), 9)
        self.assertEqual(run("2 * 3 % 4"), 2)
        self.assertEqual(run("10 - 3 - 2"), 5)

    def test_division(self):
        self.assertEqual(run("7 / 2"), 3.5)
        self.assertEqual(run("7 // 2"), 3)
        self.assertEqual(run("-7 // 2"), -4)   # unary binds tighter than //
        self.assertEqual(run("7 % 3"), 1)
        self.assertEqual(run("-7 % 3"), 2)

    def test_unary(self):
        self.assertEqual(run("- -5"), 5)
        self.assertEqual(run("not not 1"), True)
        self.assertEqual(run("not 1 == 2"), True)   # not (1 == 2)
        self.assertEqual(run("-(3 + 4)"), -7)

    def test_floats(self):
        self.assertEqual(run("1.5 + 2.5"), 4.0)
        self.assertEqual(run("1e3"), 1000.0)

    def test_comparisons(self):
        self.assertEqual(run("1 < 2"), True)
        self.assertEqual(run('"abc" > "ab"'), True)
        self.assertEqual(run("1 == 1.0"), True)
        self.assertEqual(run("[1, 2] == [1, 2]"), True)
        self.assertEqual(run("null == null"), True)
        self.assertEqual(run("1 != 2"), True)


class TestStringsAndLists(unittest.TestCase):
    def test_string_concat(self):
        self.assertEqual(run('"foo" + "bar"'), "foobar")

    def test_escapes(self):
        self.assertEqual(run('"a\\n\\t\\\\\\""'), 'a\n\t\\"')
        self.assertEqual(run('"\\u0041\\u00e9"'), "A\u00e9")

    def test_list_literal_and_concat(self):
        self.assertEqual(run("[]"), [])
        self.assertEqual(run("[1, 2] + [3]"), [1, 2, 3])
        self.assertEqual(run("[1, [2, [3]]]"), [1, [2, [3]]])

    def test_string_repeat_operator_rejected(self):
        # repetition must go through the budgeted repeat() builtin
        with self.assertRaises(ExprError):
            run('"ab" * 3')


class TestLaziness(unittest.TestCase):
    def test_short_circuit_dead_branches(self):
        self.assertEqual(run("false and (1 / 0 == 0)"), False)
        self.assertEqual(run("true or (1 / 0)"), True)
        self.assertEqual(run("if(false, 1 / 0, 42)"), 42)
        self.assertEqual(run("if(true, 42, unknownvar)"), 42)

    def test_and_or_return_operands(self):
        self.assertEqual(run("0 and 1"), 0)
        self.assertEqual(run("2 and 3"), 3)
        self.assertEqual(run('0 or "x"'), "x")
        self.assertEqual(run('"" or []'), [])

    def test_dead_branch_consumes_no_budget(self):
        ex = Executor()
        live = ex.execute("false or true")
        dead = ex.execute("true or repeat(\"x\", 100)")
        self.assertLess(dead.eval_consumed, live.eval_consumed + 2)


class TestVariables(unittest.TestCase):
    def test_variables(self):
        self.assertEqual(run("x * 2 + y", {"x": 3, "y": 1}), 7)
        self.assertEqual(run("s + s", {"s": "ab"}), "abab")

    def test_unknown_variable(self):
        with self.assertRaises(ExprError) as cm:
            run("x + 1")
        self.assertEqual(cm.exception.message, "unknown variable 'x'")

    def test_variables_are_copied(self):
        env = {"lst": [1, 2]}
        run("lst + [3]", env)
        self.assertEqual(env["lst"], [1, 2])


class TestBuiltins(unittest.TestCase):
    def test_collection_builtins(self):
        self.assertEqual(run("len([1, 2, 3])"), 3)
        self.assertEqual(run("sum([1, 2, 3])"), 6)
        self.assertEqual(run("range(3)"), [0, 1, 2])
        self.assertEqual(run("range(1, 8, 2)"), [1, 3, 5, 7])
        self.assertEqual(run("reverse([1, 2])"), [2, 1])
        self.assertEqual(run("sort([3, 1, 2])"), [1, 2, 3])
        self.assertEqual(run("get([10, 20], -1)"), 20)
        self.assertEqual(run("slice([1, 2, 3, 4], 1, 3)"), [2, 3])

    def test_string_builtins(self):
        self.assertEqual(run('upper("ab")'), "AB")
        self.assertEqual(run('lower("Ab")'), "ab")
        self.assertEqual(run('strip(" a ")'), "a")
        self.assertEqual(run('join(split("a,b,c", ","), "-")'), "a-b-c")
        self.assertEqual(run('contains("abc", "b")'), True)
        self.assertEqual(run('startswith("abc", "ab")'), True)
        self.assertEqual(run('repeat("ab", 3)'), "ababab")
        self.assertEqual(run('concat("a", "b")'), "ab")

    def test_conversion_builtins(self):
        self.assertEqual(run("str(12)"), "12")
        self.assertEqual(run("str(true)"), "true")
        self.assertEqual(run("str([1, [2]])"), "[1, [2]]")
        self.assertEqual(run('int("42")'), 42)
        self.assertEqual(run("int(3.9)"), 3)
        self.assertEqual(run("float(2)"), 2.0)
        self.assertEqual(run("bool([])"), False)
        self.assertEqual(run("min(3, 1, 2)"), 1)
        self.assertEqual(run("max(3, 1, 2)"), 3)
        self.assertEqual(run("abs(-5)"), 5)

    def test_builtin_type_errors(self):
        for src in ("len(1)", "sum([1, \"a\"])", "get([1], 5)",
                    "range(1, 2, 0)", "int(\"abc\")", "repeat(1, 2)"):
            with self.assertRaises(ExprError, msg=src):
                run(src)


class TestParseErrors(unittest.TestCase):
    def test_parse_errors(self):
        cases = ["", "1 +", "(1", "[1, 2", "f(1", "1 2", "(1, 2)",
                 "if(1, 2)", "1 @ 2", '"abc', "1 if 2", ",", ")"]
        for src in cases:
            with self.assertRaises(ParseError, msg=repr(src)):
                run(src)

    def test_if_arity(self):
        with self.assertRaises(ParseError):
            run("if(true, 1)")


if __name__ == "__main__":
    unittest.main()
