import unittest

from minisearch import tokenize


class TestTokenizer(unittest.TestCase):
    def test_latin_runs_and_casefold(self):
        self.assertEqual(tokenize("Hello, WORLD!"), ["hello", "world"])

    def test_cjk_unigrams(self):
        self.assertEqual(tokenize("机器学习"), ["机", "器", "学", "习"])

    def test_mixed_cjk_latin(self):
        self.assertEqual(
            tokenize("使用Python3.11写代码"),
            ["使", "用", "python3", "11", "写", "代", "码"],
        )

    def test_nfkc_normalization(self):
        # 全角 ＡＢＣ 归一化为半角后 casefold
        self.assertEqual(tokenize("ＡＢＣ"), ["abc"])

    def test_punctuation_and_whitespace_are_separators(self):
        self.assertEqual(tokenize("a\tb\nc-d_e"), ["a", "b", "c", "d", "e"])

    def test_empty(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("，。！"), [])


if __name__ == "__main__":
    unittest.main()
