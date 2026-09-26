"""分词与规范化规则的单元测试。"""

import unittest

from editindex.tokenizer import is_word_char, normalize_query, tokenize, vocabulary

NFD_CAFE = "cafe\u0301"     # cafe + U+0301（NFD 分解形式）
NFC_CAFE = "caf\u00e9"      # café 预组合（NFC）
Q_WITH_DOT = "q\u0307"      # q + U+0307（无预组合形式）
COMBINING_DOT = "\u0301"    # U+0301 组合符


class TestTokenize(unittest.TestCase):
    def test_splits_on_punctuation_and_whitespace(self):
        self.assertEqual(
            tokenize("Hello, 世界!  foo_bar\tbaz..."),
            ["hello", "世界", "foo", "bar", "baz"],
        )

    def test_nfc_folds_combining_accents(self):
        # NFD 形式（e + U+0301）与 NFC 预组合形式规范后相同
        self.assertEqual(tokenize(NFD_CAFE), ["café"])
        self.assertEqual(tokenize(NFD_CAFE), tokenize(NFC_CAFE))

    def test_casefold_normalization(self):
        self.assertEqual(tokenize("CAFÉ Café café"), ["café"] * 3)
        # casefold 比 lower 更彻底：ß → ss
        self.assertEqual(tokenize("STRASSE"), ["strasse"])
        self.assertEqual(tokenize("straße"), ["strasse"])

    def test_combining_mark_without_precomposed_form_stays(self):
        # q + U+0307 无预组合形式，附加符号类别 Mn 仍算词字符
        self.assertEqual(tokenize(Q_WITH_DOT), [Q_WITH_DOT])
        self.assertTrue(is_word_char(COMBINING_DOT))

    def test_emoji_and_digits(self):
        # emoji（类别 So）是分隔符；数字是词字符
        self.assertEqual(tokenize("a🐱b🎉c"), ["a", "b", "c"])
        self.assertEqual(tokenize("版本2和3"), ["版本2和3"])

    def test_query_normalization_matches_tokenizer(self):
        self.assertEqual(normalize_query("Caf\u00e9"), "caf\u00e9")


class TestVocabulary(unittest.TestCase):
    def test_dedup_stable_first_occurrence(self):
        tokens = tokenize("B a b A a C")
        self.assertEqual(vocabulary(tokens), ["b", "a", "c"])

    def test_duplicate_normalized_forms_merge(self):
        # NFC / NFD / 大小写变体归为同一规范词，只出现一次
        vocab = vocabulary(tokenize(f"{NFC_CAFE} {NFD_CAFE} CAFÉ"))
        self.assertEqual(vocab, ["café"])


if __name__ == "__main__":
    unittest.main()
