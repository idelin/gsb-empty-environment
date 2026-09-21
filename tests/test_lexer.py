import unittest

from minihm.errors import LexError
from minihm.lexer import lex
from minihm.source import Source


class LexerTest(unittest.TestCase):
    def test_spans_are_utf8_byte_ranges(self):
        text = "let λ = fun x -> x in λ 1"
        src = Source(text)
        tokens = lex(src)
        encoded = text.encode("utf-8")
        for tok in tokens:
            raw = encoded[tok.span.start:tok.span.end].decode("utf-8")
            self.assertEqual(raw, tok.text)
        lam = tokens[1]
        self.assertEqual(lam.text, "λ")
        # "let " is 4 bytes; λ is 2 UTF-8 bytes -> span 4..6
        self.assertEqual((lam.span.start, lam.span.end), (4, 6))
        # the final `1` sits after multi-byte chars; byte offset != char offset
        last_int = [t for t in tokens if t.kind == "INT"][-1]
        self.assertEqual((last_int.span.start, last_int.span.end), (26, 27))

    def test_token_kinds_and_comments(self):
        tokens = lex(Source("# hi\nlet x = 1 + 2"))
        kinds = [t.kind for t in tokens]
        self.assertEqual(kinds, ["KW", "IDENT", "OP", "INT", "OP", "INT", "EOF"])

    def test_unexpected_character(self):
        with self.assertRaises(LexError) as ctx:
            lex(Source("1 @ 2"))
        self.assertEqual((ctx.exception.span.start, ctx.exception.span.end), (2, 3))


if __name__ == "__main__":
    unittest.main()
