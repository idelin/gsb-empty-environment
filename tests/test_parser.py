import unittest

from minihm.astnodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Var
from minihm.errors import ParseError

from .helpers import parse


class ParserTest(unittest.TestCase):
    def test_precedence(self):
        ast = parse("1 + 2 * 3")
        self.assertIsInstance(ast, BinOp)
        self.assertEqual(ast.op, "+")
        self.assertIsInstance(ast.right, BinOp)
        self.assertEqual(ast.right.op, "*")

    def test_application_is_left_associative(self):
        ast = parse("f x y")
        self.assertIsInstance(ast, App)
        self.assertIsInstance(ast.fn, App)
        self.assertIsInstance(ast.arg, Var)

    def test_fun_body_extends_as_far_right_as_possible(self):
        ast = parse("fun x -> x + 1")
        self.assertIsInstance(ast, Lam)
        self.assertIsInstance(ast.body, BinOp)

    def test_let_if_fun_spans_cover_source(self):
        code = "let f = fun x -> if x then 1 else 2 in f 3"
        ast = parse(code)
        self.assertEqual((ast.span.start, ast.span.end), (0, len(code)))
        self.assertIsInstance(ast, Let)
        self.assertIsInstance(ast.value, Lam)
        self.assertIsInstance(ast.value.body, If)
        self.assertIsInstance(ast.body, App)

    def test_literals(self):
        ast = parse("(true)")
        self.assertIsInstance(ast, BoolLit)
        self.assertTrue(ast.value)
        ast = parse("42")
        self.assertIsInstance(ast, IntLit)
        self.assertEqual(ast.value, 42)

    def test_parse_error_has_span(self):
        with self.assertRaises(ParseError) as ctx:
            parse("let = 1")
        self.assertEqual((ctx.exception.span.start, ctx.exception.span.end), (4, 5))

    def test_trailing_garbage_rejected(self):
        with self.assertRaises(ParseError):
            parse("1 2 )")


if __name__ == "__main__":
    unittest.main()
