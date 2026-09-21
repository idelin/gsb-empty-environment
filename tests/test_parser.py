import unittest

from minilang import parse_source
from minilang.nodes import Assign, Bin, If, Neg, Num, Print, Var, While
from minilang.parser import ParseError


class TestParser(unittest.TestCase):
    def test_assign_span(self):
        prog = parse_source("x = 1 + 2;")
        stmt = prog.body[0]
        self.assertIsInstance(stmt, Assign)
        self.assertEqual(stmt.span, (0, 10))
        self.assertEqual(stmt.name_span, (0, 1))
        self.assertIsInstance(stmt.value, Bin)
        self.assertEqual(stmt.value.span, (4, 9))

    def test_precedence(self):
        prog = parse_source("x = 1 + 2 * 3;")
        value = prog.body[0].value
        self.assertEqual(value.op, "+")
        self.assertIsInstance(value.right, Bin)
        self.assertEqual(value.right.op, "*")

    def test_unary_minus(self):
        prog = parse_source("x = -1;")
        self.assertIsInstance(prog.body[0].value, Neg)

    def test_if_else_and_while(self):
        prog = parse_source("if (a < 1) { b = 1; } else { b = 2; } while (b > 0) { b = b - 1; }")
        if_stmt, while_stmt = prog.body
        self.assertIsInstance(if_stmt, If)
        self.assertEqual(len(if_stmt.then_body), 1)
        self.assertEqual(len(if_stmt.else_body), 1)
        self.assertIsInstance(while_stmt, While)
        self.assertIsInstance(while_stmt.cond, Bin)

    def test_print(self):
        prog = parse_source("print x;")
        self.assertIsInstance(prog.body[0], Print)
        self.assertIsInstance(prog.body[0].value, Var)

    def test_parse_error_span(self):
        with self.assertRaises(ParseError) as ctx:
            parse_source("x = ;")
        self.assertEqual((ctx.exception.start, ctx.exception.end), (4, 5))


if __name__ == "__main__":
    unittest.main()
