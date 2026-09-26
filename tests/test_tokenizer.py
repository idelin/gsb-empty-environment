import unittest

from fusion.tokenizer import tokenize


class TokenizerRulesTest(unittest.TestCase):
    def test_ascii_runs_are_single_lowercased_tokens(self):
        self.assertEqual(tokenize("Hello WORLD"), ["hello", "world"])

    def test_digits_are_ascii_alnum(self):
        self.assertEqual(tokenize("ML2024"), ["ml2024"])

    def test_underscore_and_punctuation_are_separators(self):
        self.assertEqual(tokenize("ML_2024,ok!"), ["ml", "2024", "ok"])

    def test_cjk_chars_are_unigram_tokens(self):
        self.assertEqual(tokenize("机器学习"), ["机", "器", "学", "习"])

    def test_mixed_documented_example(self):
        self.assertEqual(
            tokenize("Hello, 世界! ML_2024"),
            ["hello", "世", "界", "ml", "2024"],
        )

    def test_non_ascii_non_cjk_chars_are_separators(self):
        # Latin-1 accented letters produce no token (documented rule).
        self.assertEqual(tokenize("café"), ["caf"])

    def test_empty_and_separator_only(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("  ,、。"), [])

    def test_utf8_full_width_punctuation_separates(self):
        self.assertEqual(tokenize("机器，学习。"), ["机", "器", "学", "习"])

    def test_rejects_non_str(self):
        with self.assertRaises(TypeError):
            tokenize(b"bytes")


if __name__ == "__main__":
    unittest.main()
