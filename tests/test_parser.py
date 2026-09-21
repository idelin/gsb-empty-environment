import unittest

from callbyneed.ast_nodes import BinOp, IntLit, Let, Neg, Var
from callbyneed.errors import ParseError
from callbyneed.parser import parse


class TestParser(unittest.TestCase):
    def test_precedence(self):
        node = parse("1 + 2 * 3")
        self.assertIsInstance(node, BinOp)
        self.assertEqual(node.op, "+")
        self.assertIsInstance(node.right, BinOp)
        self.assertEqual(node.right.op, "*")

    def test_left_associativity(self):
        node = parse("1 - 2 - 3")
        self.assertEqual(node.op, "-")
        self.assertIsInstance(node.left, BinOp)
        self.assertIsInstance(node.right, IntLit)

    def test_parens(self):
        node = parse("(1 + 2) * 3")
        self.assertEqual(node.op, "*")
        self.assertIsInstance(node.left, BinOp)
        self.assertEqual(node.left.op, "+")

    def test_unary_minus(self):
        node = parse("-x * 2")
        self.assertEqual(node.op, "*")
        self.assertIsInstance(node.left, Neg)
        self.assertIsInstance(node.left.operand, Var)

    def test_let_bindings(self):
        node = parse("let x = 1, y = x + 2 in y")
        self.assertIsInstance(node, Let)
        self.assertEqual([b.name for b in node.bindings], ["x", "y"])
        self.assertIsInstance(node.body, Var)
        self.assertEqual(node.span, (0, 25))

    def test_nested_let(self):
        node = parse("let x = let y = 2 in y * 3 in x")
        self.assertIsInstance(node, Let)
        self.assertIsInstance(node.bindings[0].expr, Let)

    def test_spans_cover_source(self):
        node = parse("1 + 23")
        self.assertEqual(node.span, (0, 6))
        self.assertEqual(node.right.span, (4, 6))

    def test_parse_error_has_span(self):
        with self.assertRaises(ParseError) as ctx:
            parse("let x = in x")
        self.assertIn("bytes 8..10", str(ctx.exception))

    def test_trailing_garbage_rejected(self):
        with self.assertRaises(ParseError):
            parse("1 2")


if __name__ == "__main__":
    unittest.main()
