import unittest

from minilang.errors import LangError
from minilang.lexer import tokenize


def kinds(source):
    return [t.kind for t in tokenize(source)]


class LexerTest(unittest.TestCase):
    def test_basic_tokens(self):
        self.assertEqual(
            kinds('let x = 1 + 2.5;'),
            ["let", "IDENT", "ASSIGN", "NUMBER", "PLUS", "NUMBER", "SEMI",
             "EOF"])

    def test_keywords_and_operators(self):
        self.assertEqual(
            kinds("fn if else while return break continue throw try catch "
                  "finally true false null == != <= >= && || ! = < >"),
            ["fn", "if", "else", "while", "return", "break", "continue",
             "throw", "try", "catch", "finally", "true", "false", "null",
             "EQ", "NE", "LE", "GE", "AND", "OR", "BANG", "ASSIGN", "LT",
             "GT", "EOF"])

    def test_number_values(self):
        toks = tokenize("1 2.5")
        self.assertEqual(toks[0].value, 1)
        self.assertIsInstance(toks[0].value, int)
        self.assertEqual(toks[1].value, 2.5)
        self.assertIsInstance(toks[1].value, float)

    def test_string_escapes(self):
        toks = tokenize(r'"a\nb\t\"q\"\\"')
        self.assertEqual(toks[0].value, 'a\nb\t"q"\\')

    def test_comments_skipped(self):
        self.assertEqual(kinds("// hi\n1 /* multi\nline */ + 2"),
                         ["NUMBER", "PLUS", "NUMBER", "EOF"])

    def test_spans_are_utf8_byte_offsets(self):
        # "中" is 3 bytes in UTF-8; tokens after it must use byte offsets.
        source = 'let s = "中"; s;'
        toks = tokenize(source)
        string_tok = toks[3]
        self.assertEqual(string_tok.kind, "STRING")
        self.assertEqual((string_tok.start, string_tok.end), (8, 13))
        self.assertEqual(source.encode("utf-8")[8:13].decode("utf-8"), '"中"')
        semi = toks[4]
        self.assertEqual((semi.start, semi.end), (13, 14))
        trailing_ident = toks[5]
        self.assertEqual((trailing_ident.start, trailing_ident.end), (15, 16))

    def test_error_span_uses_bytes(self):
        source = 'let 中 = @;'
        with self.assertRaises(LangError) as ctx:
            tokenize(source)
        # '@' sits after "let " (4) + "中" (3) + " = " (3) bytes.
        self.assertEqual(ctx.exception.span, (10, 11))

    def test_unterminated_string(self):
        with self.assertRaises(LangError):
            tokenize('"abc')

    def test_unterminated_block_comment(self):
        with self.assertRaises(LangError):
            tokenize("/* never ends")


if __name__ == "__main__":
    unittest.main()
