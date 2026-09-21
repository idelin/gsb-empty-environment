import unittest

from callbyneed.errors import LexError
from callbyneed.lexer import tokenize


class TestLexer(unittest.TestCase):
    def test_token_kinds_and_spans(self):
        tokens = tokenize("let x = 12 + 3 in x")
        kinds = [t.kind for t in tokens]
        self.assertEqual(
            kinds,
            ["LET", "IDENT", "EQUALS", "INT", "PLUS", "INT", "IN", "IDENT", "EOF"],
        )
        self.assertEqual(tokens[3].text, "12")
        self.assertEqual(tokens[3].span, (8, 10))
        self.assertEqual(tokens[-1].span, (19, 19))

    def test_spans_are_utf8_byte_offsets(self):
        # '# é\n' occupies 1 + 1 + 2 + 1 = 5 bytes; '1' is at bytes 5..6
        src = "# é\n1"
        tokens = tokenize(src)
        self.assertEqual(tokens[0].kind, "INT")
        self.assertEqual(tokens[0].span, (5, 6))
        self.assertEqual(src.encode("utf-8")[5:6], b"1")

    def test_multibyte_identifier_prefix_shifts_offsets(self):
        src = "# 注释\nx"  # 注释 = 6 bytes; 'x' at byte 9..10
        tokens = tokenize(src)
        self.assertEqual(tokens[0].kind, "IDENT")
        self.assertEqual(tokens[0].span, (9, 10))

    def test_comments_and_whitespace_are_skipped(self):
        tokens = tokenize("  # hello\n  42  # bye\n")
        self.assertEqual([t.kind for t in tokens], ["INT", "EOF"])

    def test_unexpected_character_raises_with_span(self):
        with self.assertRaises(LexError) as ctx:
            tokenize("1 @ 2")
        self.assertEqual(ctx.exception.span, (2, 3))
        self.assertIn("bytes 2..3", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
