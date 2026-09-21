import unittest

from minilang import ast_nodes as ast
from minilang.errors import LangError
from minilang.lexer import tokenize
from minilang.parser import Parser


def parse(source):
    return Parser(tokenize(source)).parse_program()


class ParserTest(unittest.TestCase):
    def test_precedence(self):
        prog = parse("let x = 1 + 2 * 3;")
        init = prog.body[0].init
        self.assertIsInstance(init, ast.Binary)
        self.assertEqual(init.op, "+")
        self.assertEqual(init.right.op, "*")

    def test_unary_binds_tighter_than_binary(self):
        prog = parse("let x = -1 + 2;")
        init = prog.body[0].init
        self.assertIsInstance(init.left, ast.Unary)

    def test_logical_operators(self):
        prog = parse("let x = a || b && c;")
        init = prog.body[0].init
        self.assertIsInstance(init, ast.Logical)
        self.assertEqual(init.op, "||")
        self.assertEqual(init.right.op, "&&")

    def test_else_if_chains(self):
        prog = parse("if (a) { } else if (b) { } else { }")
        stmt = prog.body[0]
        self.assertIsInstance(stmt.otherwise, ast.If)
        self.assertIsInstance(stmt.otherwise.otherwise, ast.Block)

    def test_try_requires_catch_or_finally(self):
        with self.assertRaises(LangError):
            parse("try { }")

    def test_try_full_form(self):
        prog = parse("try { } catch (e) { } finally { }")
        stmt = prog.body[0]
        self.assertEqual(stmt.catch_name, "e")
        self.assertIsNotNone(stmt.catch_body)
        self.assertIsNotNone(stmt.finally_body)

    def test_node_spans_cover_source(self):
        source = "let x = 1;"
        prog = parse(source)
        stmt = prog.body[0]
        self.assertEqual(source[stmt.start:stmt.end], source)

    def test_syntax_error_reports_span(self):
        with self.assertRaises(LangError) as ctx:
            parse("let = 1;")
        self.assertIsNotNone(ctx.exception.span)

    def test_return_without_value(self):
        prog = parse("fn f() { return; }")
        ret = prog.body[0].body.body[0]
        self.assertIsNone(ret.value)

    def test_call_arguments(self):
        prog = parse("f(1, 2)(3);")
        call = prog.body[0].expr
        self.assertIsInstance(call, ast.Call)
        self.assertEqual(len(call.args), 1)
        self.assertEqual(len(call.callee.args), 2)


if __name__ == "__main__":
    unittest.main()
