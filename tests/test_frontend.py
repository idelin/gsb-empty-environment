"""Lexer / parser tests, with focus on UTF-8 byte spans."""

import unittest

from minilang import ast_nodes as ast
from minilang.lexer import LexError, tokenize
from minilang.parser import ParseError, parse


class LexerTest(unittest.TestCase):
    def test_simple_tokens_have_byte_spans(self):
        tokens = tokenize("let x = 42;")
        kinds = [(t.kind, t.text, t.start, t.end) for t in tokens]
        self.assertEqual(
            kinds,
            [
                ("KEYWORD", "let", 0, 3),
                ("IDENT", "x", 4, 5),
                ("PUNCT", "=", 6, 7),
                ("INT", "42", 8, 10),
                ("PUNCT", ";", 10, 11),
                ("EOF", "", 11, 11),
            ],
        )

    def test_spans_are_utf8_byte_offsets_not_char_offsets(self):
        source = "# 变量注释\nlet x = 1;"
        tokens = tokenize(source)
        let_tok = tokens[0]
        self.assertEqual(let_tok.text, "let")
        # 4 个 CJK 字符 = 12 字节，加 '#'、空格、换行共 15 字节
        self.assertEqual(let_tok.start, len("# 变量注释\n".encode("utf-8")))
        self.assertEqual(let_tok.start, 15)
        # 每个 token 的区间都能按字节切回原词
        for tok in tokens:
            raw = source.encode("utf-8")[tok.start : tok.end].decode("utf-8")
            self.assertEqual(raw, tok.text)

    def test_multibyte_inside_line_shifts_following_spans(self):
        source = "let α = 1;"  # 标识符允许非 ASCII 字母；'α' 占 2 个 UTF-8 字节
        tokens = tokenize(source)
        ident = tokens[1]
        self.assertEqual((ident.kind, ident.text), ("IDENT", "α"))
        self.assertEqual(ident.span(), (4, 6))  # 字节区间，而非字符区间 [4,5)
        eq = tokens[2]
        self.assertEqual(eq.start, 7)  # 后续 token 的偏移按字节累计

    def test_multi_char_operators(self):
        tokens = tokenize("a==b != c<=d >= e")
        ops = [t.text for t in tokens if t.kind == "OP"]
        self.assertEqual(ops, ["==", "!=", "<=", ">="])

    def test_unexpected_character_reports_span(self):
        with self.assertRaises(LexError) as ctx:
            tokenize("let x = @;")
        self.assertEqual(ctx.exception.span, (8, 9))


class ParserTest(unittest.TestCase):
    def test_precedence(self):
        prog = parse("let x = 1 + 2 * 3;")
        let = prog.body[0]
        self.assertIsInstance(let, ast.Let)
        self.assertIsInstance(let.expr, ast.BinOp)
        self.assertEqual(let.expr.op, "+")
        self.assertEqual(let.expr.right.op, "*")

    def test_unary_minus_binds_tighter_than_mul(self):
        prog = parse("let x = -2 * 3;")
        mul = prog.body[0].expr
        self.assertEqual(mul.op, "*")
        self.assertIsInstance(mul.left, ast.Neg)

    def test_statements(self):
        prog = parse("let a = 1; a = a + 1; if a > 1 { output a; } else { output 0; } while a < 10 { a = a + 1; }")
        kinds = [type(s).__name__ for s in prog.body]
        self.assertEqual(kinds, ["Let", "Assign", "If", "While"])
        if_stmt = prog.body[2]
        self.assertEqual(len(if_stmt.then_body), 1)
        self.assertEqual(len(if_stmt.else_body), 1)

    def test_input_expression(self):
        prog = parse("let a = input(0) + input(1);")
        expr = prog.body[0].expr
        self.assertIsInstance(expr.left, ast.Input)
        self.assertEqual(expr.right.index, 1)

    def test_ast_nodes_carry_byte_spans(self):
        source = "# 注释\nlet x = 1;"
        prog = parse(source)
        let = prog.body[0]
        self.assertEqual(let.span[0], len("# 注释\n".encode("utf-8")))
        raw = source.encode("utf-8")[let.span[0] : let.span[1]].decode("utf-8")
        self.assertEqual(raw, "let x = 1;")

    def test_parse_error_reports_byte_span(self):
        with self.assertRaises(ParseError) as ctx:
            parse("let = 3;")
        self.assertEqual(ctx.exception.span, (4, 5))

    def test_unterminated_block(self):
        with self.assertRaises(ParseError):
            parse("if 1 { output 1;")


if __name__ == "__main__":
    unittest.main()
