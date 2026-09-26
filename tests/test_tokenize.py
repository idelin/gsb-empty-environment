import unittest

from invidx.tokenize import tokenize


class TestTokenize(unittest.TestCase):
    def test_basic_tokens_and_offsets(self):
        tokens = tokenize("Hello, World!")
        self.assertEqual([t.term for t in tokens], ["hello", "world"])
        self.assertEqual([t.pos for t in tokens], [0, 1])
        self.assertEqual((tokens[0].start, tokens[0].end), (0, 5))
        self.assertEqual("Hello, World!"[tokens[1].start : tokens[1].end], "World")

    def test_casefold(self):
        tokens = tokenize("STRASSE Straße")
        self.assertEqual([t.term for t in tokens], ["strasse", "strasse"])
        self.assertEqual([t.pos for t in tokens], [0, 1])

    def test_unicode_and_separators(self):
        # 下划线、标点、emoji 都是分隔符；中日韩字符按 isalnum 成词
        tokens = tokenize("snake_case 中文词汇 🎉 3.14")
        self.assertEqual(
            [t.term for t in tokens], ["snake", "case", "中文词汇", "3", "14"]
        )

    def test_empty_and_separator_only(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("  --__  "), [])


if __name__ == "__main__":
    unittest.main()

