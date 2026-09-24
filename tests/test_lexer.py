import unittest

from recordlang.errors import LangError
from recordlang.lexer import Lexer


def lex(src):
    return Lexer(src).tokenize()


class LexerTest(unittest.TestCase):
    def test_kinds_and_spans(self):
        tokens = lex("let x = 42;")
        self.assertEqual(
            [t.kind for t in tokens], ["let", "IDENT", "=", "INT", ";", "EOF"]
        )
        self.assertEqual(tokens[1].span, (4, 5))
        self.assertEqual(tokens[3].span, (8, 10))

    def test_utf8_byte_spans(self):
        src = "let café = 1;\ncafé;"
        tokens = lex(src)
        ident = [t for t in tokens if t.kind == "IDENT"][1]
        prefix = "let café = 1;\ncafé"
        self.assertEqual(
            ident.span,
            (len("let café = 1;\n".encode("utf-8")), len(prefix.encode("utf-8"))),
        )
        sliced = src.encode("utf-8")[ident.span[0]:ident.span[1]].decode("utf-8")
        self.assertEqual(sliced, "café")

    def test_multibyte_comment_shifts_bytes_not_chars(self):
        src = "// 🎉🎉\n7;"
        tokens = lex(src)
        start = len("// 🎉🎉\n".encode("utf-8"))
        self.assertEqual(tokens[0].kind, "INT")
        self.assertEqual(tokens[0].span, (start, start + 1))

    def test_unexpected_character(self):
        with self.assertRaises(LangError):
            lex("let x = $;")


if __name__ == "__main__":
    unittest.main()
