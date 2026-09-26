import unittest

from topk.tokenizer import normalize, tokenize, token_set


class TestTokenizer(unittest.TestCase):
    def test_ascii_words_lowercased(self):
        self.assertEqual(tokenize("Hello World"), ["hello", "world"])

    def test_cjk_unigram(self):
        self.assertEqual(tokenize("索引归并"), ["索", "引", "归", "并"])

    def test_mixed_text_and_punctuation_separators(self):
        self.assertEqual(
            tokenize("TopK归并，支持UTF-8文本！"),
            ["topk", "归", "并", "支", "持", "utf", "8", "文", "本"],
        )

    def test_nfkc_fullwidth_folded(self):
        # 全角字母数字折叠为半角后再小写
        self.assertEqual(tokenize("ＴＯＰＫ２０２４"), ["topk2024"])
        self.assertEqual(normalize("ＡＢＣ１２３"), "abc123")

    def test_numbers(self):
        self.assertEqual(tokenize("版本 3.11 发布"), ["版", "本", "3", "11", "发", "布"])

    def test_none_and_empty(self):
        self.assertEqual(tokenize(None), [])
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("！！！　"), [])

    def test_token_set_dedup(self):
        self.assertEqual(token_set("索 索 引"), {"索", "引"})


if __name__ == "__main__":
    unittest.main()
