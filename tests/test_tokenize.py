"""分词规则测试（规则见 uniedit/tokenize.py 文档字符串）。"""
import unittest

from uniedit import tokenize


def norms(text):
    return [t.norm for t in tokenize(text)]


class TestTokenize(unittest.TestCase):
    def test_punctuation_and_space_split(self):
        self.assertEqual(norms("Hello, world!"), ["hello", "world"])

    def test_apostrophe_hyphen_underscore_split(self):
        self.assertEqual(norms("don't stop believing"), ["don", "t", "stop", "believing"])
        self.assertEqual(norms("café-au-lait"), ["café", "au", "lait"])
        self.assertEqual(norms("snake_case"), ["snake", "case"])

    def test_casefold_normalization(self):
        self.assertEqual(norms("Straße STRASSE"), ["strasse", "strasse"])
        self.assertEqual(norms("APPLE"), ["apple"])

    def test_combining_marks_stay_in_word(self):
        # 组合序列 e+U+0301 经 NFC 后与预组合 é 相同
        self.assertEqual(norms("café"), norms("café"))
        # 无预组合形式的组合记号（q + U+0301）仍属同一词元
        self.assertEqual(norms("q\u0301x"), ["q\u0301x"])

    def test_cjk_run_is_one_token(self):
        self.assertEqual(norms("中文 测试"), ["中文", "测试"])

    def test_digits_in_word(self):
        self.assertEqual(norms("abc123 def"), ["abc123", "def"])

    def test_emoji_are_separators(self):
        self.assertEqual(norms("hi\U0001F600there"), ["hi", "there"])

    def test_positions(self):
        toks = tokenize("a bb ccc")
        self.assertEqual([t.position for t in toks], [0, 1, 2])
        self.assertEqual([t.surface for t in toks], ["a", "bb", "ccc"])


if __name__ == "__main__":
    unittest.main()
