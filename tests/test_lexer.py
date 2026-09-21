import unittest

from minilang.lexer import LexError, lex


class LexerTests(unittest.TestCase):
    def test_utf8_byte_spans_with_multibyte_comment(self):
        src = "// 注释 ok\nlet x = 1;"
        data = src.encode("utf-8")
        tokens = lex(src)
        for tok in tokens:
            self.assertEqual(data[tok.start : tok.end].decode("utf-8"), tok.text)
        let_tok = next(t for t in tokens if t.text == "let")
        # "// 注释 ok\n" is 3 + 6 + 3 + 1 = 13 bytes, not 10 characters
        self.assertEqual(let_tok.start, len("// 注释 ok\n".encode("utf-8")))
        self.assertEqual(let_tok.start, 13)
        x_tok = next(t for t in tokens if t.text == "x")
        self.assertEqual((x_tok.start, x_tok.end), (17, 18))

    def test_token_kinds(self):
        tokens = lex("let ab_2 = 42;")
        kinds = [t.kind for t in tokens]
        self.assertEqual(kinds, ["keyword", "ident", "punct", "int", "punct", "eof"])

    def test_two_char_operators(self):
        texts = [t.text for t in lex("a == b != c <= d >= e < f > g") if t.kind == "punct"]
        self.assertEqual(texts, ["==", "!=", "<=", ">=", "<", ">"])

    def test_non_ascii_outside_comment_rejected(self):
        with self.assertRaises(LexError) as ctx:
            lex("let π = 1;")
        # byte offset of the first byte of π (2 bytes in UTF-8)
        self.assertEqual(ctx.exception.start, 4)
        self.assertEqual(ctx.exception.end, 5)

    def test_unexpected_character(self):
        with self.assertRaises(LexError):
            lex("let x = @;")
