"""分词规则测试。"""

from __future__ import annotations

import unittest

from minisearch.tokenizer import tokenize


class TokenizerTest(unittest.TestCase):
    def test_basic_latin_and_numbers(self) -> None:
        self.assertEqual(tokenize("Wireless Mouse 42"), ["wireless", "mouse", "42"])

    def test_punctuation_split(self) -> None:
        self.assertEqual(tokenize("Wi-Fi, usb-c."), ["wi", "fi", "usb", "c"])

    def test_underscore_is_separator(self) -> None:
        self.assertEqual(tokenize("keyboard_42"), ["keyboard", "42"])

    def test_cjk_unigrams(self) -> None:
        self.assertEqual(tokenize("机械键盘"), ["机", "械", "键", "盘"])

    def test_mixed_cjk_latin(self) -> None:
        self.assertEqual(
            tokenize("mechanical keyboard 机械键盘 青轴"),
            ["mechanical", "keyboard", "机", "械", "键", "盘", "青", "轴"],
        )

    def test_casefold_and_nfkc(self) -> None:
        self.assertEqual(tokenize("ßtraße"), ["sstrasse"])
        self.assertEqual(tokenize("ＵＳＢ"), ["usb"])

    def test_empty_and_non_string(self) -> None:
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("--- !!! ，。"), [])
        with self.assertRaises(TypeError):
            tokenize(b"utf8 bytes")


if __name__ == "__main__":
    unittest.main()
