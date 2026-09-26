import unittest

from search_engine import tokenize


class TokenizeTest(unittest.TestCase):
    def test_cjk_chars_become_single_tokens(self):
        self.assertEqual(tokenize("苹果手机"), ["苹", "果", "手", "机"])

    def test_casefold_latin(self):
        self.assertEqual(tokenize("Apple APPLE apple"), ["apple"] * 3)

    def test_alnum_runs_and_separators(self):
        self.assertEqual(
            tokenize("Python3.11, hello-world!"),
            ["python3", "11", "hello", "world"],
        )

    def test_mixed_cjk_and_latin(self):
        self.assertEqual(
            tokenize("使用Python写搜索引擎"),
            ["使", "用", "python", "写", "搜", "索", "引", "擎"],
        )

    def test_empty_and_punctuation_only(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("，。！ ……"), [])


if __name__ == "__main__":
    unittest.main()
