import unittest

from minilang.ast_nodes import Assign, BinOp, Halt, If, IntLit, Let, Print, Var, While
from minilang.parser import ParseError, parse


class ParserTests(unittest.TestCase):
    def test_precedence(self):
        (stmt,) = parse("print 1 + 2 * 3;")
        self.assertIsInstance(stmt, Print)
        expr = stmt.value
        self.assertIsInstance(expr, BinOp)
        self.assertEqual(expr.op, "+")
        self.assertIsInstance(expr.right, BinOp)
        self.assertEqual(expr.right.op, "*")

    def test_statement_spans_are_byte_ranges(self):
        src = "let x = 1;\nprint x;"
        let_stmt, print_stmt = parse(src)
        self.assertEqual(src.encode()[let_stmt.start : let_stmt.end], b"let x = 1;")
        self.assertEqual(src.encode()[print_stmt.start : print_stmt.end], b"print x;")

    def test_if_while_halt(self):
        program = parse("if 1 { let a = 2; } else { halt; } while 0 { print 1; }")
        self.assertIsInstance(program[0], If)
        self.assertIsInstance(program[0].then_body[0], Let)
        self.assertIsInstance(program[0].else_body[0], Halt)
        self.assertIsInstance(program[1], While)

    def test_assignment_and_expression_spans(self):
        (stmt,) = parse("total = total + i;")
        self.assertIsInstance(stmt, Assign)
        self.assertEqual(stmt.name, "total")
        rhs = stmt.value
        self.assertIsInstance(rhs, BinOp)
        self.assertIsInstance(rhs.left, Var)
        self.assertEqual((rhs.left.start, rhs.left.end), (8, 13))

    def test_integer_literal_value(self):
        (stmt,) = parse("let n = 007;")
        self.assertIsInstance(stmt.value, IntLit)
        self.assertEqual(stmt.value.value, 7)

    def test_parse_error_has_span(self):
        with self.assertRaises(ParseError) as ctx:
            parse("let = 1;")
        self.assertEqual((ctx.exception.start, ctx.exception.end), (4, 5))

    def test_unterminated_block(self):
        with self.assertRaises(ParseError):
            parse("if 1 { print 1;")
