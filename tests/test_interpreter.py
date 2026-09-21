import unittest

from minilang import EvalError, evaluate, parse


def eval_lets(src):
    tree, errors = parse(src)
    assert not errors, errors
    return evaluate(tree)


class TestInterpreter(unittest.TestCase):
    def test_arithmetic_and_precedence(self):
        self.assertEqual(eval_lets("let a = 2 + 3 * 4;")["a"], 14)
        self.assertEqual(eval_lets("let a = (2 + 3) * 4;")["a"], 20)
        self.assertEqual(eval_lets("let a = 10 / 4;")["a"], 2.5)
        self.assertEqual(eval_lets("let a = -3 + 1;")["a"], -2)
        self.assertEqual(eval_lets("let a = 1.5 * 2;")["a"], 3.0)

    def test_function_call(self):
        src = "def double(x) = x + x;\ndef area(r) = 3 * double(r) * r;\nlet a = area(2);"
        self.assertEqual(eval_lets(src)["a"], 24)

    def test_let_chain(self):
        self.assertEqual(eval_lets("let a = 1;\nlet b = a + 41;")["b"], 42)

    def test_undefined_variable(self):
        with self.assertRaises(EvalError):
            eval_lets("let a = missing + 1;")

    def test_undefined_function(self):
        with self.assertRaises(EvalError):
            eval_lets("let a = nope(1);")

    def test_division_by_zero(self):
        with self.assertRaises(EvalError):
            eval_lets("let a = 1 / 0;")

    def test_arity_mismatch(self):
        with self.assertRaises(EvalError):
            eval_lets("def f(x) = x;\nlet a = f(1, 2);")

    def test_recursion_depth_guard(self):
        with self.assertRaises(EvalError):
            eval_lets("def f(n) = f(n);\nlet a = f(1);")

    def test_syntax_error_program_rejected(self):
        tree, errors = parse("let a = (1;")
        self.assertTrue(errors)
        with self.assertRaises(EvalError):
            evaluate(tree)

    def test_error_message_has_byte_range(self):
        tree, _ = parse("let a = 1;\nlet b = unknown;")
        with self.assertRaises(EvalError) as ctx:
            evaluate(tree)
        self.assertIn("字节", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
