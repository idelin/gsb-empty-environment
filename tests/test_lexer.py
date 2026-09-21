import unittest

from minilang.lexer import LexError, tokenize


class TestLexer(unittest.TestCase):
    def test_basic_tokens_and_byte_spans(self):
        toks = tokenize("x = 12;")
        self.assertEqual(
            [(t.kind, t.text, t.start, t.end) for t in toks],
            [("ID", "x", 0, 1), ("OP", "=", 2, 3), ("NUM", "12", 4, 6),
             ("OP", ";", 6, 7), ("EOF", "", 7, 7)],
        )

    def test_utf8_byte_offsets(self):
        # '#'(1) ' '(1) '注'(3) '释'(3) '\n'(1) => 'x' 从字节 9 开始
        toks = tokenize("# 注释\nx = 1;")
        x = next(t for t in toks if t.text == "x")
        self.assertEqual((x.start, x.end), (9, 10))
        one = next(t for t in toks if t.text == "1")
        self.assertEqual((one.start, one.end), (13, 14))

    def test_keywords_and_two_char_ops(self):
        toks = tokenize("while (a <= 1) { a = a + 1; }")
        self.assertEqual(toks[0].kind, "KW")
        le = next(t for t in toks if t.text == "<=")
        self.assertEqual(le.kind, "OP")

    def test_comments_and_eof(self):
        toks = tokenize("# 只有注释\n")
        self.assertEqual([t.kind for t in toks], ["EOF"])

    def test_lex_error_has_byte_span(self):
        with self.assertRaises(LexError) as ctx:
            tokenize("a = $;")
        self.assertEqual((ctx.exception.start, ctx.exception.end), (4, 5))


if __name__ == "__main__":
    unittest.main()
