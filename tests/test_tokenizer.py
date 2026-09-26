import unittest

from indexing.tokenizer import term_counts, tokenize


class TokenizeTest(unittest.TestCase):
    def test_ascii_words_are_lowercased_runs(self):
        self.assertEqual(tokenize("Hello, WORLD! py3.11"), ["hello", "world", "py3", "11"])

    def test_cjk_chars_are_unigram_tokens(self):
        self.assertEqual(tokenize("搜索引擎"), ["搜", "索", "引", "擎"])

    def test_mixed_text_and_punctuation_separators(self):
        self.assertEqual(
            tokenize("Python 支持 UTF-8，是的!"),
            ["python", "支", "持", "utf", "8", "是", "的"],
        )

    def test_underscore_is_a_separator(self):
        self.assertEqual(tokenize("a_b"), ["a", "b"])

    def test_bytes_are_decoded_as_utf8(self):
        self.assertEqual(tokenize("世界".encode("utf-8")), ["世", "界"])

    def test_invalid_utf8_raises(self):
        with self.assertRaises(UnicodeDecodeError):
            tokenize(b"\xff\xfe")

    def test_term_counts(self):
        self.assertEqual(
            term_counts("hello world hello 世界 世界"),
            {"hello": 2, "world": 1, "世": 2, "界": 2},
        )


if __name__ == "__main__":
    unittest.main()
