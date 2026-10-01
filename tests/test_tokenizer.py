import unittest

from ranker.tokenizer import tokenize


class TestTokenizer(unittest.TestCase):
    """分词规则 unicode-unigram-v1 的行为固定。"""

    def test_ascii_runs_lowercased(self):
        self.assertEqual(tokenize("Hello World"), ["hello", "world"])

    def test_cjk_unigrams(self):
        self.assertEqual(tokenize("机器学习"), ["机", "器", "学", "习"])

    def test_mixed_and_separators(self):
        self.assertEqual(tokenize("Python3.11 机器学习!"),
                         ["python3", "11", "机", "器", "学", "习"])

    def test_non_ascii_alnum_is_unigram(self):
        self.assertEqual(tokenize("café"), ["caf", "é"])

    def test_nfc_normalization(self):
        self.assertEqual(tokenize("é"), tokenize("é"))

    def test_duplicates_kept_for_tf(self):
        self.assertEqual(tokenize("a a b"), ["a", "a", "b"])

    def test_punctuation_only(self):
        self.assertEqual(tokenize("，。！…"), [])


if __name__ == "__main__":
    unittest.main()
